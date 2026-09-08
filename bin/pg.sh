#!/usr/bin/env bash
# Deploy and operate the PostgreSQL cluster.
#
#   ./bin/pg.sh deploy demo      # 1 instance, no backups
#   ./bin/pg.sh deploy prod      # 3 instances across zones + GCS backups
#   ./bin/pg.sh status
#   ./bin/pg.sh password         # app user credentials
#   ./bin/pg.sh psql             # interactive psql on the current primary
#   ./bin/pg.sh scale <n>        # change instance count (read replicas)
#   ./bin/pg.sh backup           # trigger an on-demand base backup
#   ./bin/pg.sh pooler           # deploy PgBouncer in front of the primary
#   ./bin/pg.sh destroy          # delete the Cluster (PVCs go with it)
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh

ensure_namespace() {
  kubectl get namespace "$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$NAMESPACE"
}

deploy() {
  local profile="${1:?usage: pg.sh deploy <demo|prod>}"
  ensure_namespace
  case "$profile" in
    demo)
      render templates/pg.demo.yml | kubectl apply -f -
      ;;
    prod)
      require_project
      kubectl apply -f templates/storageclass.hyperdisk.yml
      # ObjectStore must exist before the cluster starts archiving WAL to it.
      render templates/backup.yml | kubectl apply -f -
      render templates/pg.prod.yml | kubectl apply -f -
      ;;
    *) echo "unknown profile: $profile (expected demo|prod)" >&2; exit 1 ;;
  esac
  echo "==> Waiting for cluster to become ready (this can take a few minutes)"
  kubectl -n "$NAMESPACE" wait --for=condition=Ready "cluster/${PG_CLUSTER}" --timeout 600s
  status
}

status() {
  kubectl -n "$NAMESPACE" get cluster "$PG_CLUSTER" -o wide
  kubectl -n "$NAMESPACE" get pods -l "cnpg.io/cluster=${PG_CLUSTER}" -o wide
  kubectl -n "$NAMESPACE" get svc -l "cnpg.io/cluster=${PG_CLUSTER}"
}

password() {
  local user pass
  user=$(kubectl -n "$NAMESPACE" get secret "${PG_CLUSTER}-app" -o jsonpath='{.data.username}' | base64 -d)
  pass=$(kubectl -n "$NAMESPACE" get secret "${PG_CLUSTER}-app" -o jsonpath='{.data.password}' | base64 -d)
  echo "host:     ${PG_CLUSTER}-rw.${NAMESPACE}.svc.cluster.local (read-write)"
  echo "          ${PG_CLUSTER}-ro.${NAMESPACE}.svc.cluster.local (read-only)"
  echo "database: app"
  echo "username: ${user}"
  echo "password: ${pass}"
}

psql_primary() {
  local primary
  primary=$(kubectl -n "$NAMESPACE" get cluster "$PG_CLUSTER" -o jsonpath='{.status.currentPrimary}')
  if [[ -z "$primary" ]]; then
    echo "ERROR: no current primary for ${PG_CLUSTER} (still bootstrapping or mid-failover?)" >&2
    exit 1
  fi
  kubectl -n "$NAMESPACE" exec -it "$primary" -c postgres -- psql -U postgres "$@"
}

scale() {
  local n="$1"
  kubectl -n "$NAMESPACE" patch cluster "$PG_CLUSTER" --type merge \
    -p "{\"spec\":{\"instances\":${n}}}"
  echo "==> Scaled ${PG_CLUSTER} to ${n} instances; watch with: ./bin/pg.sh status"
}

backup() {
  local name; name="${PG_CLUSTER}-manual-$(date +%Y%m%d%H%M%S)"
  kubectl -n "$NAMESPACE" apply -f - <<EOF
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata:
  name: ${name}
spec:
  cluster:
    name: ${PG_CLUSTER}
  method: plugin
  pluginConfiguration:
    name: barman-cloud.cloudnative-pg.io
EOF
  echo "==> Backup ${name} requested; watch with: kubectl -n ${NAMESPACE} get backup ${name} -w"
}

pooler() {
  render templates/pooler.yml | kubectl apply -f -
  kubectl -n "$NAMESPACE" get pooler
}

destroy() {
  kubectl -n "$NAMESPACE" delete cluster "$PG_CLUSTER" --ignore-not-found
}

case "${1:-}" in
  deploy)   deploy "${2:-}" ;;
  status)   status ;;
  password) password ;;
  psql)     shift; psql_primary "$@" ;;
  scale)    scale "${2:?usage: pg.sh scale <instances>}" ;;
  backup)   backup ;;
  pooler)   pooler ;;
  destroy)  destroy ;;
  *) echo "usage: $0 {deploy <demo|prod>|status|password|psql|scale <n>|backup|pooler|destroy}" >&2; exit 1 ;;
esac
