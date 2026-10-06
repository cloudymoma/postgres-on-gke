#!/usr/bin/env bash
# Metrics and alerting: Google Managed Prometheus scrapes the CloudNativePG
# instance pods (templates/monitoring.yml) and Cloud Monitoring alert policies
# (templates/alerts/*.yml, PromQL) watch them.
#
#   ./bin/monitoring.sh setup    # PodMonitoring + alert policies (idempotent)
#   ./bin/monitoring.sh status
#   ./bin/monitoring.sh clean    # delete this cluster's alert policies + PodMonitoring
#
# ALERT_EMAIL=you@example.com ./bin/monitoring.sh setup also creates (or
# reuses) an email notification channel and attaches it to new policies.
# Without it, alerts only open incidents in the Cloud Monitoring console.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh
require_project

ALERT_EMAIL="${ALERT_EMAIL:-}"

# Prints the name of an email channel for ALERT_EMAIL, creating it if needed.
email_channel() {
  local ch
  ch=$(gcloud beta monitoring channels list --project "$PROJECT_ID" \
    --filter "type=email AND labels.email_address=\"${ALERT_EMAIL}\"" \
    --format 'value(name)' | head -n 1)
  if [[ -z "$ch" ]]; then
    ch=$(gcloud beta monitoring channels create --project "$PROJECT_ID" \
      --display-name "postgres-on-gke: ${ALERT_EMAIL}" \
      --type email --channel-labels "email_address=${ALERT_EMAIL}" \
      --format 'value(name)')
  fi
  echo "$ch"
}

# Names of this cluster's alert policies.
cluster_policies() {
  gcloud monitoring policies list --project "$PROJECT_ID" \
    --filter "userLabels.pg_cluster=\"${PG_CLUSTER}\" AND userLabels.pg_namespace=\"${NAMESPACE}\"" \
    --format 'value(name)'
}

setup() {
  echo "==> Applying PodMonitoring ${NAMESPACE}/${PG_CLUSTER}"
  render templates/monitoring.yml | kubectl apply -f -

  local channel_flag=()
  if [[ -n "$ALERT_EMAIL" ]]; then
    channel_flag=(--notification-channels "$(email_channel)")
    echo "==> Notifying ${ALERT_EMAIL} (${channel_flag[1]})"
  fi

  echo "==> Creating alert policies"
  local existing f tmp dn
  existing=$(gcloud monitoring policies list --project "$PROJECT_ID" --format 'value(displayName)')
  for f in templates/alerts/*.yml; do
    tmp=$(mktemp)
    render "$f" >"$tmp"
    dn=$(sed -n 's/^displayName: "\(.*\)"$/\1/p' "$tmp")
    if grep -Fxq "$dn" <<<"$existing"; then
      echo "    exists:  ${dn}"
    else
      # ${arr[@]+...}: bash 3.2 treats an empty array as unbound under set -u.
      gcloud monitoring policies create --project "$PROJECT_ID" \
        --policy-from-file "$tmp" ${channel_flag[@]+"${channel_flag[@]}"} >/dev/null
      echo "    created: ${dn}"
    fi
    rm -f "$tmp"
  done
  echo "==> Existing policies are left unchanged; run clean then setup to update them."
}

status() {
  kubectl -n "$NAMESPACE" get podmonitoring "$PG_CLUSTER"
  gcloud monitoring policies list --project "$PROJECT_ID" \
    --filter "userLabels.pg_cluster=\"${PG_CLUSTER}\" AND userLabels.pg_namespace=\"${NAMESPACE}\"" \
    --format 'table(displayName,severity,enabled)'
}

clean() {
  local p
  for p in $(cluster_policies); do
    gcloud monitoring policies delete "$p" --project "$PROJECT_ID" --quiet
  done
  kubectl -n "$NAMESPACE" delete podmonitoring "$PG_CLUSTER" --ignore-not-found
}

case "${1:-}" in
  setup)  setup ;;
  status) status ;;
  clean)  clean ;;
  *) echo "usage: $0 {setup|status|clean}" >&2; exit 1 ;;
esac
