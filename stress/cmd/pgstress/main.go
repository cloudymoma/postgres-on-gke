// pgstress is a multi-threaded PostgreSQL load generator with a
// self-contained HTML report. See ../../README.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
	"github.com/binwu/postgres-on-gke/stress/internal/engine"
	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
	"github.com/binwu/postgres-on-gke/stress/internal/report"
	"github.com/binwu/postgres-on-gke/stress/internal/workload"
)

const doneMarker = "PGSTRESS_DONE"

var logger = log.New(os.Stderr, "pgstress ", log.LstdFlags|log.Lmsgprefix)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: pgstress <command> [flags]

  prepare  -scenario f [-rw-host h]                       create the pgstress schema and data
  run      -scenario f [-rw-host h] [-ro-host h] [-out d]  run the scenario, write results.json + report.html
           [-prepare] [-cleanup] [-hold d]
  cleanup  [-rw-host h]                                   drop the pgstress schema
  report   -out report.html a.json [b.json ...]           render (and compare) result files
  cat      <file>                                         write a file to stdout (for kubectl exec)

Connection parameters other than host come from PGUSER, PGPASSWORD,
PGDATABASE, PGSSLMODE (standard libpq environment variables).
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "prepare":
		err = cmdPrepare(ctx, os.Args[2:])
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "cleanup":
		err = cmdCleanup(ctx, os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "cat":
		err = cmdCat(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		logger.Fatalf("error: %v", err)
	}
}

func hostFlags(fs *flag.FlagSet) (rw, ro *string) {
	rw = fs.String("rw-host", envOr("PGSTRESS_RW_HOST", "localhost"), "primary (read-write) host")
	ro = fs.String("ro-host", envOr("PGSTRESS_RO_HOST", ""), "replica (read-only) host; defaults to rw-host")
	return
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func cmdPrepare(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	scPath := fs.String("scenario", "", "scenario file")
	rwHost, _ := hostFlags(fs)
	fs.Parse(args)
	sc, err := config.Load(*scPath)
	if err != nil {
		return err
	}
	pool, err := engine.NewPool(ctx, *rwHost, 4, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	if sc.Prepare != config.PrepareTPCB {
		return fmt.Errorf("scenario %q has prepare: %s; nothing to prepare", sc.Name, sc.Prepare)
	}
	logger.Printf("preparing TPC-B schema at scale %d", sc.Scale)
	return workload.PrepareTPCB(ctx, sc.Scale, pool, logger.Printf)
}

func cmdCleanup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	rwHost, _ := hostFlags(fs)
	fs.Parse(args)
	pool, err := engine.NewPool(ctx, *rwHost, 2, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Printf("dropping schema %s", workload.Schema)
	return workload.Cleanup(ctx, pool)
}

func checkConnLimit(ctx context.Context, pool *pgxpool.Pool, host string, sc *config.Scenario) error {
	var mcStr, srStr string
	if err := pool.QueryRow(ctx, "SHOW max_connections").Scan(&mcStr); err != nil {
		return fmt.Errorf("query max_connections on %s: %w", host, err)
	}
	if err := pool.QueryRow(ctx, "SHOW superuser_reserved_connections").Scan(&srStr); err != nil {
		return fmt.Errorf("query superuser_reserved_connections on %s: %w", host, err)
	}
	maxConns, err := strconv.Atoi(mcStr)
	if err != nil {
		return fmt.Errorf("parse max_connections %q on %s: %w", mcStr, host, err)
	}
	reserved, err := strconv.Atoi(srStr)
	if err != nil {
		return fmt.Errorf("parse superuser_reserved_connections %q on %s: %w", srStr, host, err)
	}
	usable := maxConns - reserved
	needed := sc.MaxWorkers() + 4
	if needed > usable {
		return fmt.Errorf(
			"host %s allows %d non-superuser connections (max_connections=%d, superuser_reserved_connections=%d), "+
				"but scenario %q requires %d (max workers %d + 4 headroom); "+
				"remedies: lower stages[].workers or raise max_connections (and re-check work_mem)",
			host, usable, maxConns, reserved, sc.Name, needed, sc.MaxWorkers(),
		)
	}
	return nil
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	scPath := fs.String("scenario", "", "scenario file")
	out := fs.String("out", ".", "output directory")
	doPrepare := fs.Bool("prepare", false, "run prepare before the test")
	doCleanup := fs.Bool("cleanup", false, "drop the pgstress schema after the test")
	hold := fs.Duration("hold", 0, "after finishing, print "+doneMarker+" and wait this long (or until SIGTERM) so files can be copied out")
	rwHost, roHost := hostFlags(fs)
	fs.Parse(args)
	if *roHost == "" {
		*roHost = *rwHost
	}

	sc, err := config.Load(*scPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}

	// Admin pool has no statement_timeout so DDL (PrepareTPCB / Cleanup) and
	// startup connection probes are never cancelled by a tight scenario timeout.
	admin, err := engine.NewPool(ctx, *rwHost, 2, 0)
	if err != nil {
		return err
	}
	defer admin.Close()

	if err := checkConnLimit(ctx, admin, *rwHost, sc); err != nil {
		return err
	}
	if *roHost != *rwHost {
		roProbe, err := engine.NewPool(ctx, *roHost, 1, 0)
		if err != nil {
			return err
		}
		err = checkConnLimit(ctx, roProbe, *roHost, sc)
		roProbe.Close()
		if err != nil {
			return err
		}
	}

	maxConns := sc.MaxWorkers() + 4
	rw, err := engine.NewPool(ctx, *rwHost, maxConns, sc.StatementTimeout)
	if err != nil {
		return err
	}
	defer rw.Close()
	ro := rw
	if *roHost != *rwHost {
		if ro, err = engine.NewPool(ctx, *roHost, maxConns, sc.StatementTimeout); err != nil {
			return err
		}
		defer ro.Close()
	}

	if *doPrepare && sc.Prepare == config.PrepareTPCB {
		logger.Printf("preparing TPC-B schema at scale %d", sc.Scale)
		if err := workload.PrepareTPCB(ctx, sc.Scale, admin, logger.Printf); err != nil {
			return err
		}
	}

	eng, err := engine.New(sc, rw, ro, logger)
	if err != nil {
		return err
	}
	logger.Printf("scenario %q: %d stages, %s total, max %d workers", sc.Name, len(sc.Stages), sc.TotalDuration(), sc.MaxWorkers())
	res, runErr := eng.Run(ctx)
	if runErr != nil {
		logger.Printf("run ended early: %v", runErr)
	}
	logger.Printf("done: %d tx, %.0f tx/s, p50 %.2f ms, p99 %.2f ms, %d errors",
		res.Overall.Count, res.Overall.TPS, res.Overall.P50, res.Overall.P99, res.Overall.Errors)

	if *doCleanup && sc.Prepare == config.PrepareTPCB {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if err := workload.Cleanup(cctx, admin); err != nil {
			logger.Printf("cleanup failed: %v", err)
		}
		cancel()
	}

	jsonPath := filepath.Join(*out, "results.json")
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(jsonPath, b, 0o644); err != nil {
		return err
	}
	htmlPath := filepath.Join(*out, "report.html")
	f, err := os.Create(htmlPath)
	if err != nil {
		return err
	}
	if err := report.Render(f, []*metrics.Results{res}); err != nil {
		f.Close()
		return err
	}
	f.Close()
	logger.Printf("wrote %s and %s", jsonPath, htmlPath)

	if *hold > 0 {
		fmt.Println(doneMarker)
		logger.Printf("holding for %s so files can be copied out (SIGTERM to exit)", *hold)
		select {
		case <-time.After(*hold):
		case <-ctx.Done():
		}
	}
	return nil
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	out := fs.String("out", "report.html", "output HTML file")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("report: at least one results.json is required")
	}
	runs, err := report.LoadResults(fs.Args())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := report.Render(f, runs); err != nil {
		return err
	}
	logger.Printf("wrote %s (%d run(s))", *out, len(runs))
	return nil
}

func cmdCat(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("cat: exactly one file")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}
