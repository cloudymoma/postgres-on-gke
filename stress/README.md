# pgstress: load and stress testing for postgres-on-gke

A standalone, multi-threaded Go load generator that runs **inside the GKE
cluster** as a Kubernetes Job against the CloudNativePG cluster from the
parent repo, and produces a **self-contained HTML report** (client-side
throughput/latency/errors plus server-side `pg_stat_*` metrics).

Its purpose is to answer: *what happens to throughput and latency when I
scale this cluster?*

## How it works

```
run.sh run scenarios/tpcb.yaml
  │  1. ConfigMap with the scenario
  │  2. Job (k8s/job.yaml) → pod runs: pgstress run -prepare -cleanup -hold 15m
  │        ├─ creates pgstress schema (TPC-B tables) in the app database
  │        ├─ N worker goroutines per stage, closed loop, weighted statements
  │        │    rw statements → <cluster>-rw   (primary)
  │        │    ro statements → <cluster>-ro   (replicas)
  │        ├─ aggregator goroutine: HDR histograms → per-second series
  │        ├─ sampler goroutine: pg_stat_database / activity / replication, WAL
  │        └─ writes /out/results.json + /out/report.html, prints PGSTRESS_DONE
  │  3. copies both files to ./out/<scenario>-<timestamp>/ on your machine
  └─ 4. deletes the Job
```

## Prerequisites

- The parent repo's cluster is up (`make init_prod` or `init_demo`) and
  `kubectl` points at it; `../config.sh` holds your project/region/names.
- `docker`, `go` ≥ 1.26 (only for `report` compare and tests), `envsubst`.
- Artifact Registry API enabled (`run.sh build` creates the `pgstress` repo).

## Quickstart

```bash
cd stress
./run.sh build                       # build + push image (once, and after code changes)
./run.sh grant                       # once: GRANT pg_monitor TO app -> full server stats
./run.sh run scenarios/tpcb.yaml     # ~6 min ramp; report lands in out/tpcb-<ts>/report.html
open out/tpcb-*/report.html
```

Then scale the database and run again:

```bash
../bin/pg.sh scale 5                 # more replicas
./run.sh run scenarios/read-heavy.yaml
./run.sh report out/read-heavy-*/results.json   # overlays every run in one report
```

Options: `CPU=4 MEMORY=2Gi ./run.sh run …` sizes the load-generator pod
(default 2 CPU / 1 GiB). `KEEP=1` leaves the Job for inspection.

## Scenarios

| File | What it measures |
|---|---|
| `scenarios/tpcb.yaml` | pgbench-style TPC-B write transaction on the primary, 10→200 worker ramp |
| `scenarios/read-heavy.yaml` | 90% point reads on `-ro` (replicas), 10% updates on `-rw`; shows replica scaling |
| `scenarios/custom-example.yaml` | Template for your own schema: `prepare: none`, choice args, multi-query transactions |

Scenario format:

```yaml
name: my-test
prepare: tpcb | none        # tpcb = create/drop pgstress.* tables at `scale` (100k accounts per unit)
scale: 10
sample_interval: 5s         # pg_stat_* poll interval
statement_timeout: 30s
stages:                     # a ramp; a single stage is a classic fixed-concurrency benchmark
  - {workers: 10, duration: 60s}
statements:
  - name: read
    weight: 80              # relative frequency
    target: ro              # rw (primary) | ro (replicas)
    sql: SELECT ... WHERE id = $1
    args:
      - {name: id, type: int, min: 1, max: 1000000}       # or max_per_scale: 100000
      - {name: kind, type: choice, choices: [a, b, c]}
  - name: txn               # several queries in one transaction
    args: [...]
    queries:
      - {sql: "UPDATE ... $1 ... $2", params: [delta, id]}
      - {sql: "INSERT ... $1",        params: [id]}
```

If `prepare: tpcb` and no `statements` are given, the built-in TPC-B
transaction is used.

## Reading the report

- **Runs / Stages tables**: the stage where tx/s stops growing while p99
  keeps rising is your saturation point for that cluster size.
- **Throughput / Latency / Errors over time**: shaded bands mark stages.
- **Per-statement p99**: which statement degrades first.
- **Server panels**: connections by state, commit/rollback rate, buffer
  cache hit ratio, replication lag per replica, WAL MiB/s. If the app user
  is not in `pg_monitor`, the report says so and some panels are partial.
- **Load generator CPU**: if this is above ~80%, the client, not the
  database, is the bottleneck. Raise `CPU=`.

Errors never abort a run; they are counted per statement by SQLSTATE.

## Development

```bash
make test           # unit tests with -race (no database needed)
make vet
docker run -d --rm -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18
PGHOST=localhost PGUSER=postgres make integration
go run ./cmd/pgstress run -scenario scenarios/tpcb.yaml -rw-host localhost -prepare -cleanup -out out/local
```

Layout:

```
cmd/pgstress/        CLI (prepare | run | cleanup | report | cat)
internal/config/     scenario YAML, defaults, validation
internal/workload/   TPC-B schema + data, statement compilation, weighted picker
internal/engine/     pools, stage controller, worker goroutines
internal/metrics/    HDR histograms, per-second series, summaries, results types
internal/sampler/    pg_stat_* poller
internal/report/     results JSON -> HTML (Chart.js vendored, MIT)
k8s/job.yaml         Job template rendered by run.sh
```
