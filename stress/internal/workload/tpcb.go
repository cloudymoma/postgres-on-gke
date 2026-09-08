package workload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/binwu/postgres-on-gke/stress/internal/config"
)

// Schema is the dedicated schema that holds all pgstress tables.
const Schema = "pgstress"

const (
	accountsPerScale = 100_000
	tellersPerScale  = 10
	branchesPerScale = 1
)

// TPCBStatements is the pgbench default transaction expressed as a scenario
// statement, sized for the given scale factor.
func TPCBStatements(scale int) []config.Statement {
	s := int64(scale)
	return []config.Statement{{
		Name:   "tpcb",
		Weight: 1,
		Target: config.TargetRW,
		Args: []config.Arg{
			{Name: "aid", Type: "int", Min: 1, Max: accountsPerScale * s},
			{Name: "bid", Type: "int", Min: 1, Max: branchesPerScale * s},
			{Name: "tid", Type: "int", Min: 1, Max: tellersPerScale * s},
			{Name: "delta", Type: "int", Min: -5000, Max: 5000},
		},
		Queries: []config.Query{
			{SQL: "UPDATE pgstress.accounts SET abalance = abalance + $1 WHERE aid = $2", Params: []string{"delta", "aid"}},
			{SQL: "SELECT abalance FROM pgstress.accounts WHERE aid = $1", Params: []string{"aid"}},
			{SQL: "UPDATE pgstress.tellers SET tbalance = tbalance + $1 WHERE tid = $2", Params: []string{"delta", "tid"}},
			{SQL: "UPDATE pgstress.branches SET bbalance = bbalance + $1 WHERE bid = $2", Params: []string{"delta", "bid"}},
			{SQL: "INSERT INTO pgstress.history (tid, bid, aid, delta, mtime) VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)", Params: []string{"tid", "bid", "aid", "delta"}},
		},
	}}
}

// PrepareTPCB (re)creates the schema and loads scale × 100k accounts.
func PrepareTPCB(ctx context.Context, scale int, pool *pgxpool.Pool, logf func(string, ...any)) error {
	ddl := []string{
		"DROP SCHEMA IF EXISTS " + Schema + " CASCADE",
		"CREATE SCHEMA " + Schema,
		"CREATE TABLE pgstress.branches (bid int NOT NULL, bbalance int NOT NULL DEFAULT 0, filler char(88))",
		"CREATE TABLE pgstress.tellers (tid int NOT NULL, bid int NOT NULL, tbalance int NOT NULL DEFAULT 0, filler char(84))",
		"CREATE TABLE pgstress.accounts (aid int NOT NULL, bid int NOT NULL, abalance int NOT NULL DEFAULT 0, filler char(84))",
		"CREATE TABLE pgstress.history (tid int, bid int, aid int, delta int, mtime timestamp, filler char(22))",
		fmt.Sprintf("INSERT INTO pgstress.branches (bid) SELECT g FROM generate_series(1, %d) g", branchesPerScale*scale),
		fmt.Sprintf("INSERT INTO pgstress.tellers (tid, bid) SELECT g, (g-1)/%d+1 FROM generate_series(1, %d) g", tellersPerScale, tellersPerScale*scale),
	}
	for _, q := range ddl {
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("prepare: %s: %w", q, err)
		}
	}
	for b := 1; b <= scale; b++ {
		lo := int64(b-1)*accountsPerScale + 1
		hi := int64(b) * accountsPerScale
		q := fmt.Sprintf("INSERT INTO pgstress.accounts (aid, bid) SELECT g, %d FROM generate_series(%d, %d) g", b, lo, hi)
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("prepare: load accounts %d..%d: %w", lo, hi, err)
		}
		logf("loaded accounts %d/%d", hi, int64(scale)*accountsPerScale)
	}
	post := []string{
		"ALTER TABLE pgstress.branches ADD PRIMARY KEY (bid)",
		"ALTER TABLE pgstress.tellers ADD PRIMARY KEY (tid)",
		"ALTER TABLE pgstress.accounts ADD PRIMARY KEY (aid)",
		"ANALYZE pgstress.branches", "ANALYZE pgstress.tellers", "ANALYZE pgstress.accounts",
	}
	for _, q := range post {
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("prepare: %s: %w", q, err)
		}
	}
	return nil
}

// Cleanup drops everything pgstress created.
func Cleanup(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+Schema+" CASCADE")
	return err
}
