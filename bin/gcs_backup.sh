#!/usr/bin/env bash
# One-time backup infrastructure setup: GCS bucket + Google service account
# wired to the cluster's Kubernetes service account via Workload Identity.
# Run BEFORE `./bin/pg.sh deploy prod`.
#
#   ./bin/gcs_backup.sh setup
#   ./bin/gcs_backup.sh clean    # remove bucket (and all backups!) + GSA
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh
require_project

setup() {
  echo "==> Creating bucket gs://${GCS_BUCKET}"
  gcloud storage buckets create "gs://${GCS_BUCKET}" \
    --project "$PROJECT_ID" --location "$REGION" \
    --uniform-bucket-level-access 2>/dev/null || echo "    (bucket already exists)"

  echo "==> Creating service account ${GSA_EMAIL}"
  gcloud iam service-accounts create "$GSA_NAME" \
    --project "$PROJECT_ID" --display-name "CloudNativePG backups" 2>/dev/null \
    || echo "    (service account already exists)"

  echo "==> Granting objectAdmin on the bucket"
  gcloud storage buckets add-iam-policy-binding "gs://${GCS_BUCKET}" \
    --member "serviceAccount:${GSA_EMAIL}" \
    --role roles/storage.objectAdmin >/dev/null

  # CloudNativePG creates a Kubernetes SA named after the cluster; the
  # serviceAccountTemplate annotation in templates/pg.prod.yml links it to
  # this GSA, and the binding below completes the Workload Identity pair.
  echo "==> Binding Workload Identity: ${NAMESPACE}/${PG_CLUSTER} -> ${GSA_EMAIL}"
  gcloud iam service-accounts add-iam-policy-binding "$GSA_EMAIL" \
    --project "$PROJECT_ID" \
    --role roles/iam.workloadIdentityUser \
    --member "serviceAccount:${PROJECT_ID}.svc.id.goog[${NAMESPACE}/${PG_CLUSTER}]" >/dev/null

  echo "==> Backup infrastructure ready: gs://${GCS_BUCKET}"
}

clean() {
  read -r -p "This DELETES gs://${GCS_BUCKET} including all backups. Type the bucket name to confirm: " answer
  [[ "$answer" == "$GCS_BUCKET" ]] || { echo "aborted"; exit 1; }
  gcloud storage rm -r "gs://${GCS_BUCKET}" || true
  gcloud iam service-accounts delete "$GSA_EMAIL" --project "$PROJECT_ID" --quiet || true
}

case "${1:-}" in
  setup) setup ;;
  clean) clean ;;
  *) echo "usage: $0 {setup|clean}" >&2; exit 1 ;;
esac
