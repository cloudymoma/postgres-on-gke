#!/usr/bin/env bash
# Build the image, run a scenario as an in-cluster Job, and pull the report
# back to this machine.
#
#   ./run.sh build                          # build + push image to Artifact Registry
#   ./run.sh grant                          # one-time: GRANT pg_monitor TO app (full server stats)
#   ./run.sh run scenarios/tpcb.yaml [dir]  # run; results land in dir (default out/<name>-<timestamp>)
#   ./run.sh report out/a/results.json out/b/results.json   # compare runs -> report.html
#   ./run.sh clean                          # delete leftover pgstress Jobs/ConfigMaps
#
# Env overrides: IMAGE, CPU (default 1), CPU_REQUEST (default 500m), MEMORY (default 1Gi), KEEP=1 (don't delete the Job).
set -euo pipefail
self="$(realpath "$0")"
cd "$(dirname "$self")"
# shellcheck source=../config.sh
source ../config.sh
require_project

IMAGE="${IMAGE:-${REGION}-docker.pkg.dev/${PROJECT_ID}/pgstress/pgstress:latest}"
CPU="${CPU:-1}"
CPU_REQUEST="${CPU_REQUEST:-500m}"
MEMORY="${MEMORY:-1Gi}"
export IMAGE CPU CPU_REQUEST MEMORY

build() {
  local repo_host="${REGION}-docker.pkg.dev"
  gcloud artifacts repositories describe pgstress --project "$PROJECT_ID" --location "$REGION" >/dev/null 2>&1 ||
    gcloud artifacts repositories create pgstress --project "$PROJECT_ID" --location "$REGION" \
      --repository-format docker --description "pgstress load generator"
  gcloud auth configure-docker "$repo_host" --quiet
  docker build -t "$IMAGE" .
  docker push "$IMAGE"
  echo "==> pushed $IMAGE"
}

grant() {
  ../bin/pg.sh psql -c "GRANT pg_monitor TO \"$(kubectl -n "$NAMESPACE" get secret "${PG_CLUSTER}-app" -o jsonpath='{.data.username}' | base64 -d)\";"
}

run() {
  local scenario="${1:?usage: run.sh run <scenario.yaml> [outdir]}"
  local name; name=$(basename "$scenario" .yaml)
  local stamp; stamp=$(date +%Y%m%d-%H%M%S)
  local outdir="${2:-out/${name}-${stamp}}"
  # Budget: 'pgstress-' (9) + name + '-' (1) + stamp (15) must be <= 63,
  # so the name gets at most 38 chars. Truncate the name, never the stamp,
  # and strip any trailing hyphen the cut may leave behind.
  local safe_name
  safe_name=$(echo "$name" | tr '[:upper:]_' '[:lower:]-' | cut -c1-38 | sed 's/-*$//')
  local JOB_NAME="pgstress-${safe_name}-${stamp}"
  export JOB_NAME

  mkdir -p "$outdir"
  kubectl -n "$NAMESPACE" create configmap "${JOB_NAME}-scenario" \
    --from-file=scenario.yaml="$scenario" --dry-run=client -o yaml |
    kubectl -n "$NAMESPACE" label --local -f - app=pgstress -o yaml |
    kubectl -n "$NAMESPACE" apply -f -
  envsubst '$JOB_NAME $NAMESPACE $PG_CLUSTER $IMAGE $CPU $CPU_REQUEST $MEMORY' <k8s/job.yaml | kubectl apply -f -
  echo "==> Job ${JOB_NAME} created; waiting for pod"

  local pod=""
  for _ in $(seq 1 60); do
    pod=$(kubectl -n "$NAMESPACE" get pods -l "job-name=${JOB_NAME}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    [[ -n "$pod" ]] && break
    sleep 2
  done
  [[ -n "$pod" ]] || { echo "pod never appeared"; exit 1; }
  kubectl -n "$NAMESPACE" wait --for=condition=Ready "pod/$pod" --timeout 300s

  echo "==> Streaming logs from $pod (Ctrl-C detaches, the Job keeps running)"
  kubectl -n "$NAMESPACE" logs -f "$pod" 2>&1 | sed -u '/^PGSTRESS_DONE$/q' || true

  local phase; phase=$(kubectl -n "$NAMESPACE" get pod "$pod" -o jsonpath='{.status.phase}')
  if [[ "$phase" == "Failed" ]]; then
    echo "==> Job failed; last log lines:"; kubectl -n "$NAMESPACE" logs "$pod" | tail -20; exit 1
  fi

  echo "==> Copying report to $outdir"
  kubectl -n "$NAMESPACE" exec "$pod" -- /pgstress cat /out/results.json >"$outdir/results.json.tmp"
  mv "$outdir/results.json.tmp" "$outdir/results.json"
  kubectl -n "$NAMESPACE" exec "$pod" -- /pgstress cat /out/report.html >"$outdir/report.html.tmp"
  mv "$outdir/report.html.tmp" "$outdir/report.html"

  if [[ "${KEEP:-0}" != "1" ]]; then
    kubectl -n "$NAMESPACE" delete job "$JOB_NAME" --wait=false >/dev/null
    kubectl -n "$NAMESPACE" delete configmap "${JOB_NAME}-scenario" >/dev/null
  fi
  echo "==> Done: $outdir/report.html"
}

report() {
  [[ $# -ge 1 ]] || { echo "usage: run.sh report <results.json>..."; exit 1; }
  local out; out="out/compare-$(date +%Y%m%d-%H%M%S).html"
  go run ./cmd/pgstress report -out "$out" "$@"
  echo "==> $out"
}

clean() {
  kubectl -n "$NAMESPACE" delete jobs,configmaps -l app=pgstress --ignore-not-found
}

case "${1:-}" in
  build)  build ;;
  grant)  grant ;;
  run)    shift; run "$@" ;;
  report) shift; report "$@" ;;
  clean)  clean ;;
  *) sed -n '2,11p' "$self" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
