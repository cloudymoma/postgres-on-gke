# postgres-on-gke

Deploy, manage, and scale a highly-available PostgreSQL cluster on Google
Kubernetes Engine, using the [CloudNativePG](https://cloudnative-pg.io)
operator. Modeled after
[elasticsearch-cn/elastic-on-gke](https://github.com/elasticsearch-cn/elastic-on-gke):
the operator does the hard stateful lifecycle work, this repo wraps it with
GKE provisioning, topology presets, backups, and load balancing.

**Stack** (pinned in `config.sh`):

| Component | Version |
|---|---|
| CloudNativePG operator | 1.30.0 |
| Barman Cloud plugin (backups) | 0.14.0 |
| cert-manager (plugin prerequisite) | v1.21.1 |
| PostgreSQL image | `ghcr.io/cloudnative-pg/postgresql:18` |

## Architecture

- **Demo**: zonal GKE cluster, 1 Postgres instance, no backups. For kicking tires.
- **Production**: regional GKE cluster in `us-central1` across 3 zones on
  `c4-highmem-4` nodes (5th Gen Intel Xeon Emerald Rapids + Titanium I/O engine)
  with `hyperdisk-balanced` storage, 1 primary + 2 streaming replicas pinned
  one-per-zone, automatic failover, continuous WAL archiving + daily base
  backups to GCS (Workload Identity, no key files), internal TCP load balancers
  for read-write and read-only traffic, optional PgBouncer.

The operator creates three ClusterIP services automatically:
`<cluster>-rw` (primary), `<cluster>-ro` (replicas), `<cluster>-r` (any).

## Prerequisites

- `gcloud`, `kubectl`, `envsubst` (part of gettext), `make`
- A GCP project with the GKE API enabled and quota for two `e2-standard-2` VMs
  (demo) or three `c4-highmem-4` VMs plus Hyperdisk Balanced (production)

## Quickstart (demo)

```bash
gcloud config set project <your-project>
make init_demo            # ≈ ./bin/demo.sh up  (takes ~10 min)
./bin/pg.sh psql          # interactive psql on the primary
./bin/demo.sh clean       # tear down
```

## Production walkthrough

Edit `config.sh` (or export overrides), then:

```bash
make init_prod
```

which is equivalent to:

```bash
./bin/gke.sh create           # regional GKE cluster across 3 zones
./bin/cnpg.sh install         # cert-manager + CNPG operator + Barman plugin
./bin/gcs_backup.sh setup     # GCS bucket + GSA + Workload Identity binding
./bin/pg.sh deploy prod       # ObjectStore + 3-instance Cluster
./bin/lb.sh deploy            # internal LBs for VPC-internal access
```

Get credentials for the auto-created `app` database:

```bash
./bin/pg.sh password
```

## Day-2 operations

### Scale reads (horizontal)

```bash
./bin/pg.sh scale 5           # adds streaming replicas; -ro service spreads reads
```

Note: `templates/pg.prod.yml` enforces host anti-affinity (`kubernetes.io/hostname: required`)
to guarantee 1 pod per physical node, paired with `topologySpreadConstraints` across
zones. To scale beyond 3 instances, scale the GKE node pool accordingly (`./bin/gke.sh scale 2` = 6 nodes)
so new instances find dedicated host machines.

### Scale up (vertical)

Edit `resources`/`storage` in `templates/pg.prod.yml` and re-apply
(`./bin/pg.sh deploy prod`). The operator restarts replicas first, then does
a controlled switchover — near-zero downtime. Storage can only grow, and
online only if the storage class allows volume expansion (`hyperdisk-balanced` does).

### Scale GKE nodes

```bash
./bin/gke.sh scale 2          # nodes per zone
```

### Connection pooling

```bash
./bin/pg.sh pooler            # PgBouncer (transaction pooling) at <cluster>-pooler-rw:5432
```

### Backups & point-in-time recovery

WAL archiving is continuous and a base backup runs daily at 02:00 UTC
(`templates/backup.yml`, 30-day retention). On demand:

```bash
./bin/pg.sh backup
```

Restore creates a *new* cluster from GCS (never in-place):

```bash
PG_CLUSTER=pg-main-restored ./bin/gcs_backup.sh setup   # WI binding for the new cluster
source ./config.sh && render templates/pg.restore.yml | kubectl apply -f -
```

Set `recoveryTarget.targetTime` in `templates/pg.restore.yml` for PITR.

### Upgrades

- **Postgres minor** (e.g. 18.1 → 18.2): bump `PG_IMAGE` in `config.sh`,
  re-run `./bin/pg.sh deploy prod`. Rolling update, replicas first.
- **Postgres major**: CloudNativePG supports offline in-place major upgrades
  (bump the image major version — read the release notes first), or
  blue/green via a new cluster bootstrapped from a backup.
- **Operator**: bump versions in `config.sh`, re-run `./bin/cnpg.sh install`.
  Operator upgrades don't restart database pods.

### Failover

Automatic. Test it:

```bash
kubectl -n pg delete pod pg-main-1   # a replica is promoted in seconds
./bin/pg.sh status
```

## Load / stress testing

`stress/` holds **pgstress**, a multi-threaded Go load generator that runs as
a Job inside the cluster and produces a self-contained HTML report (client
throughput / latency / errors plus `pg_stat_*` server metrics). Use it to
measure the effect of scaling:

```bash
cd stress && ./run.sh build && ./run.sh grant
./run.sh run scenarios/tpcb.yaml            # report lands in stress/out/<name>-<ts>/report.html
../bin/pg.sh scale 5 && ./run.sh run scenarios/read-heavy.yaml
./run.sh report out/*/results.json          # overlay runs in one report
```

See `stress/README.md`.

## Repo structure

```
config.sh              # single place for project, region, versions, names
bin/
  demo.sh              # one-command PoC (up / status / clean)
  gke.sh               # GKE cluster create / scale / delete
  cnpg.sh              # operator + backup plugin install (idempotent; re-run to upgrade) / status
  pg.sh                # deploy / status / password / psql / scale / backup / pooler
  gcs_backup.sh        # GCS bucket + Workload Identity setup
  lb.sh                # internal load balancers
templates/
  pg.demo.yml          # 1-instance Cluster
  pg.prod.yml          # 3-instance HA Cluster, C4 + Hyperdisk, host anti-affinity + zone spread, GCS archiving
  storageclass.hyperdisk.yml # Hyperdisk Balanced StorageClass for GKE
  backup.yml           # ObjectStore (GCS) + daily ScheduledBackup
  pg.restore.yml       # PITR: new cluster bootstrapped from GCS
  pooler.yml           # PgBouncer
  lb.yml               # internal TCP LBs (rw + ro)
terraform/gcp/         # optional IaC path for the GCP-side resources
stress/                # pgstress load generator + HTML report (see stress/README.md)
Makefile               # init_demo / init_prod presets
```

## Mapping from elastic-on-gke

| elastic-on-gke | here |
|---|---|
| ECK operator | CloudNativePG operator |
| `Elasticsearch` CRD, `nodeSets.count` | `Cluster` CRD, `instances` |
| GCS snapshot repository | Barman Cloud plugin → GCS (WAL archiving + PITR) |
| `bin/es.sh`, `bin/kbn.sh` | `bin/pg.sh` |
| `bin/upgrade_ECK.sh` | `bin/cnpg.sh install` (idempotent) |
| `make init_single` / `init_prod` | `make init_demo` / `init_prod` |
| GLB with HTTPS (HTTP protocol) | internal TCP LBs (Postgres wire protocol) |
| zone-aware shard allocation | required pod anti-affinity per host + zone spread constraints |

The key conceptual difference: Elasticsearch scales writes horizontally by
adding nodes; PostgreSQL is single-primary. Adding instances here scales
**reads** and **availability**. If you outgrow a single primary for writes,
that's a sharding conversation (e.g. Citus), not more replicas.
