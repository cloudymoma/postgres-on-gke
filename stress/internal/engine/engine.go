// Package engine runs the staged, multi-worker load loop.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
	"github.com/binwu/postgres-on-gke/stress/internal/sampler"
	"github.com/binwu/postgres-on-gke/stress/internal/workload"
)

// Engine drives one scenario against a pair of pools.
type Engine struct {
	sc     *config.Scenario
	stmts  []workload.Statement
	picker *workload.Picker
	rw, ro *pgxpool.Pool
	log    *log.Logger

	samples chan metrics.Sample
	agg     *metrics.Aggregator
}

// New compiles the scenario and prepares the engine. Pools stay owned by the caller.
func New(sc *config.Scenario, rw, ro *pgxpool.Pool, logger *log.Logger) (*Engine, error) {
	stmts, err := workload.Compile(sc)
	if err != nil {
		return nil, err
	}
	return &Engine{
		sc: sc, stmts: stmts, picker: workload.NewPicker(workload.Weights(stmts)),
		rw: rw, ro: ro, log: logger,
	}, nil
}

// Run executes every stage and returns the results. ctx cancellation ends
// the run early but still produces results for what completed.
func (e *Engine) Run(ctx context.Context) (*metrics.Results, error) {
	start := time.Now()
	e.samples = make(chan metrics.Sample, 1<<16)
	e.agg = metrics.NewAggregator(workload.Names(e.stmts), e.samples, time.Second)
	go e.agg.Run(start)

	smp := sampler.New(e.rw, e.sc.SampleInterval)
	sctx, stopSampler := context.WithCancel(ctx)
	var sampWG sync.WaitGroup
	sampWG.Add(1)
	go func() { defer sampWG.Done(); smp.Run(sctx, start) }()

	var (
		wg      sync.WaitGroup
		cancels []context.CancelFunc
	)
	setWorkers := func(n int) {
		for len(cancels) < n {
			wctx, cancel := context.WithCancel(ctx)
			cancels = append(cancels, cancel)
			wg.Add(1)
			go e.worker(wctx, &wg, len(cancels)-1)
		}
		for len(cancels) > n {
			cancels[len(cancels)-1]()
			cancels = cancels[:len(cancels)-1]
		}
	}

	var runErr error
stages:
	for i, st := range e.sc.Stages {
		e.log.Printf("stage %d/%d: %d workers for %s", i+1, len(e.sc.Stages), st.Workers, st.Duration)
		e.agg.SetStage(i, st.Workers)
		setWorkers(st.Workers)
		select {
		case <-time.After(st.Duration):
		case <-ctx.Done():
			runErr = ctx.Err()
			break stages
		}
	}
	setWorkers(0)
	wg.Wait()
	stopSampler()
	sampWG.Wait()
	close(e.samples)
	e.agg.Wait()
	end := time.Now()

	res := &metrics.Results{
		Scenario: e.sc.Name, StartedAt: start, FinishedAt: end, DurationSec: end.Sub(start).Seconds(),
	}
	for _, st := range e.sc.Stages {
		res.StagePlan = append(res.StagePlan, metrics.StagePlan{Workers: st.Workers, DurationSec: st.Duration.Seconds()})
	}
	res.Series, res.Statements, res.Stages, res.Overall = e.agg.Results(end.Sub(start))
	res.Samples, res.SamplerErrors, res.Server = smp.Results()
	res.Client = clientInfo(end.Sub(start))
	return res, runErr
}

func (e *Engine) worker(ctx context.Context, wg *sync.WaitGroup, id int) {
	defer wg.Done()
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(id)))
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		st := &e.stmts[e.picker.Pick(rng)]
		t0 := time.Now()
		err := e.exec(ctx, st, rng)
		lat := time.Since(t0)
		if err == nil {
			e.samples <- metrics.Sample{Stmt: st.Index, Latency: lat}
			backoff = 100 * time.Millisecond
			continue
		}
		if ctx.Err() != nil {
			return // cancelled mid-statement: not a real failure
		}
		code := classify(err)
		e.samples <- metrics.Sample{Stmt: st.Index, ErrCode: code}
		if code == "conn" {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}

func (e *Engine) exec(ctx context.Context, st *workload.Statement, rng *rand.Rand) error {
	pool := e.rw
	if st.Target == config.TargetRO {
		pool = e.ro
	}
	vals := make([]any, len(st.Args))
	for i, a := range st.Args {
		vals[i] = a.Gen(rng)
	}
	bind := func(q workload.Query) []any {
		p := make([]any, len(q.Params))
		for i, idx := range q.Params {
			p[i] = vals[idx]
		}
		return p
	}
	qctx, cancel := context.WithTimeout(ctx, e.sc.StatementTimeout)
	defer cancel()

	if len(st.Queries) == 1 {
		_, err := pool.Exec(qctx, st.Queries[0].SQL, bind(st.Queries[0])...)
		return err
	}
	tx, err := pool.BeginTx(qctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	for _, q := range st.Queries {
		if _, err := tx.Exec(qctx, q.SQL, bind(q)...); err != nil {
			_ = tx.Rollback(qctx)
			return err
		}
	}
	return tx.Commit(qctx)
}

// classify maps an error to a short code for the report: SQLSTATE when
// Postgres produced it, "timeout" for statement timeouts, "conn" otherwise.
func classify(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "conn"
}

func clientInfo(elapsed time.Duration) metrics.ClientInfo {
	ci := metrics.ClientInfo{NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0)}
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
		ci.CPUSeconds = float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6 +
			float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
		if elapsed > 0 {
			ci.CPUUtilization = ci.CPUSeconds / elapsed.Seconds() / float64(ci.GOMAXPROCS)
		}
	}
	return ci
}

// NewPool opens a pool for host using standard PG* environment variables
// for the remaining connection parameters.
func NewPool(ctx context.Context, host string, maxConns int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(fmt.Sprintf("host=%s application_name=pgstress", host))
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(maxConns)
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to %s: %w", host, err)
	}
	return pool, nil
}
