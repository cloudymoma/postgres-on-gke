//go:build integration

package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
	"github.com/binwu/postgres-on-gke/stress/internal/engine"
	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
	"github.com/binwu/postgres-on-gke/stress/internal/report"
	"github.com/binwu/postgres-on-gke/stress/internal/workload"
)

// TestIntegrationEndToEnd runs prepare -> two short stages -> cleanup ->
// report against the Postgres described by PGHOST/PGUSER/PGPASSWORD/PGDATABASE.
func TestIntegrationEndToEnd(t *testing.T) {
	host := os.Getenv("PGHOST")
	if host == "" {
		t.Skip("PGHOST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sc := &config.Scenario{Name: "it", Prepare: config.PrepareTPCB, Scale: 1, SampleInterval: time.Second,
		Stages: []config.Stage{{Workers: 2, Duration: 2 * time.Second}, {Workers: 4, Duration: 2 * time.Second}},
		Statements: []config.Statement{
			{Name: "read", Weight: 3, Target: config.TargetRO,
				SQL:  "SELECT abalance FROM pgstress.accounts WHERE aid = $1",
				Args: []config.Arg{{Name: "aid", Min: 1, MaxPerScale: 100000}}},
			{Name: "bad", Weight: 1, SQL: "SELECT * FROM pgstress.does_not_exist"},
		}}
	sc.Normalize()
	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}

	pool, err := engine.NewPool(ctx, host, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := workload.PrepareTPCB(ctx, 1, pool, t.Logf); err != nil {
		t.Fatal(err)
	}
	defer workload.Cleanup(context.Background(), pool)

	eng, err := engine.New(sc, pool, pool, log.New(os.Stderr, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Overall.Count == 0 {
		t.Fatal("no successful transactions recorded")
	}
	if res.Statements[1].ErrorsByCode["42P01"] == 0 {
		t.Fatalf("expected undefined_table errors for the bad statement, got %+v", res.Statements[1])
	}
	if len(res.Stages) != 2 || res.Stages[1].Workers != 4 {
		t.Fatalf("stages not recorded: %+v", res.Stages)
	}
	if res.Server.Version == "" || len(res.Samples) == 0 {
		t.Fatalf("sampler produced nothing: %+v %v", res.Server, res.SamplerErrors)
	}
	var buf bytes.Buffer
	if err := report.Render(&buf, []*metrics.Results{res}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"scenario":"it"`) {
		t.Fatal("report does not embed results")
	}
}
