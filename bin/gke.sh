#!/usr/bin/env bash
# Create / scale / delete the GKE cluster.
#
#   ./bin/gke.sh create          # regional cluster (3 zones), for production
#   ./bin/gke.sh create demo     # cheap zonal cluster, for the demo
#   ./bin/gke.sh credentials [demo] # fetch kubeconfig for an existing cluster
#   ./bin/gke.sh scale <n> [demo] # nodes (per zone for regional clusters)
#   ./bin/gke.sh status
#   ./bin/gke.sh delete [demo]
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh
require_project

create() {
  local profile="${1:-prod}"
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters create "$GKE_CLUSTER" \
      --project "$PROJECT_ID" \
      --zone "$ZONE" \
      --machine-type "$DEMO_MACHINE_TYPE" \
      --num-nodes "$DEMO_NUM_NODES" \
      --workload-pool "${PROJECT_ID}.svc.id.goog" \
      --enable-ip-alias
  else
    gcloud container clusters create "$GKE_CLUSTER" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --machine-type "$MACHINE_TYPE" \
      --num-nodes "$NUM_NODES" \
      --workload-pool "${PROJECT_ID}.svc.id.goog" \
      --enable-ip-alias
  fi
  credentials "$profile"
}

credentials() {
  local profile="${1:-prod}"
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters get-credentials "$GKE_CLUSTER" --project "$PROJECT_ID" --zone "$ZONE"
  else
    gcloud container clusters get-credentials "$GKE_CLUSTER" --project "$PROJECT_ID" --region "$REGION"
  fi
}

scale() {
  local nodes="$1" profile="${2:-prod}"
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters resize "$GKE_CLUSTER" \
      --project "$PROJECT_ID" --zone "$ZONE" \
      --num-nodes "$nodes" --quiet
  else
    gcloud container clusters resize "$GKE_CLUSTER" \
      --project "$PROJECT_ID" --region "$REGION" \
      --num-nodes "$nodes" --quiet
  fi
}

status() {
  gcloud container clusters list --project "$PROJECT_ID" --filter "name=${GKE_CLUSTER}"
  kubectl get nodes -o wide 2>/dev/null || true
}

delete() {
  local profile="${1:-prod}"
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters delete "$GKE_CLUSTER" --project "$PROJECT_ID" --zone "$ZONE" --quiet
  else
    gcloud container clusters delete "$GKE_CLUSTER" --project "$PROJECT_ID" --region "$REGION" --quiet
  fi
}

case "${1:-}" in
  create)      create "${2:-prod}" ;;
  credentials) credentials "${2:-prod}" ;;
  scale)       scale "${2:?usage: gke.sh scale <nodes-per-zone> [demo]}" "${3:-prod}" ;;
  status)      status ;;
  delete)      delete "${2:-prod}" ;;
  *) echo "usage: $0 {create [demo]|credentials [demo]|scale <n> [demo]|status|delete [demo]}" >&2; exit 1 ;;
esac
