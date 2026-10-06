#!/usr/bin/env bash
# Create / scale / delete the GKE cluster.
#
#   ./bin/gke.sh create          # regional cluster (3 zones), for production
#   ./bin/gke.sh create demo     # cheap zonal cluster, for the demo
#   ./bin/gke.sh credentials [demo] # fetch kubeconfig for an existing cluster
#   ./bin/gke.sh scale <n> [demo] # nodes (per zone for regional clusters)
#   ./bin/gke.sh maintenance [demo] # apply MAINTENANCE_START/END (config.sh) to an existing cluster
#   ./bin/gke.sh status
#   ./bin/gke.sh delete [demo]
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh
require_project

# Sets MAINT_FLAGS to the gcloud flags for a daily MAINTENANCE_START..
# MAINTENANCE_END (UTC) window. Only the time of day matters; an end at/before
# the start means the window crosses midnight.
maintenance_flags() {
  local hhmm='^([01][0-9]|2[0-3]):[0-5][0-9]$' end_day=01
  if [[ ! "$MAINTENANCE_START" =~ $hhmm || ! "$MAINTENANCE_END" =~ $hhmm ]]; then
    echo "ERROR: MAINTENANCE_START/MAINTENANCE_END must be HH:MM (UTC), got '${MAINTENANCE_START}'/'${MAINTENANCE_END}'" >&2
    exit 1
  fi
  [[ "$MAINTENANCE_END" > "$MAINTENANCE_START" ]] || end_day=02
  MAINT_FLAGS=(
    "--maintenance-window-start=2000-01-01T${MAINTENANCE_START}:00Z"
    "--maintenance-window-end=2000-01-${end_day}T${MAINTENANCE_END}:00Z"
    "--maintenance-window-recurrence=FREQ=DAILY"
  )
}

create() {
  local profile="${1:-prod}"
  maintenance_flags
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters create "$GKE_CLUSTER" \
      --project "$PROJECT_ID" \
      --zone "$ZONE" \
      --machine-type "$DEMO_MACHINE_TYPE" \
      --num-nodes "$DEMO_NUM_NODES" \
      --workload-pool "${PROJECT_ID}.svc.id.goog" \
      --enable-ip-alias \
      --enable-managed-prometheus \
      "${MAINT_FLAGS[@]}"
  else
    gcloud container clusters create "$GKE_CLUSTER" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --machine-type "$MACHINE_TYPE" \
      --disk-type "$BOOT_DISK_TYPE" \
      --num-nodes "$NUM_NODES" \
      --workload-pool "${PROJECT_ID}.svc.id.goog" \
      --enable-ip-alias \
      --enable-managed-prometheus \
      "${MAINT_FLAGS[@]}"
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

# Re-apply MAINTENANCE_START/MAINTENANCE_END to an existing cluster.
maintenance() {
  local profile="${1:-prod}"
  maintenance_flags
  if [[ "$profile" == "demo" ]]; then
    gcloud container clusters update "$GKE_CLUSTER" --project "$PROJECT_ID" --zone "$ZONE" "${MAINT_FLAGS[@]}"
  else
    gcloud container clusters update "$GKE_CLUSTER" --project "$PROJECT_ID" --region "$REGION" "${MAINT_FLAGS[@]}"
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
  maintenance) maintenance "${2:-prod}" ;;
  status)      status ;;
  delete)      delete "${2:-prod}" ;;
  *) echo "usage: $0 {create [demo]|credentials [demo]|scale <n> [demo]|maintenance [demo]|status|delete [demo]}" >&2; exit 1 ;;
esac
