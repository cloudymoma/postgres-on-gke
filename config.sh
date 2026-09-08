# shellcheck shell=bash
# Central configuration, sourced by every script in bin/.
# Override any value via environment, e.g.:  PROJECT_ID=my-proj ./bin/demo.sh

# --- GCP ---------------------------------------------------------------
PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project 2>/dev/null)}"
REGION="${REGION:-us-central1}"
ZONE="${ZONE:-${REGION}-a}"                # used by the demo (zonal) cluster

# --- GKE ---------------------------------------------------------------
GKE_CLUSTER="${GKE_CLUSTER:-pg-gke}"
MACHINE_TYPE="${MACHINE_TYPE:-c4-highmem-4}"
NUM_NODES="${NUM_NODES:-1}"                # per zone (regional cluster spans 3 zones)
DEMO_MACHINE_TYPE="${DEMO_MACHINE_TYPE:-e2-standard-2}"
DEMO_NUM_NODES="${DEMO_NUM_NODES:-2}"

# --- PostgreSQL / CloudNativePG -----------------------------------------
NAMESPACE="${NAMESPACE:-pg}"
PG_CLUSTER="${PG_CLUSTER:-pg-main}"
PG_IMAGE="${PG_IMAGE:-ghcr.io/cloudnative-pg/postgresql:18}"

CNPG_VERSION="${CNPG_VERSION:-1.30.0}"
BARMAN_PLUGIN_VERSION="${BARMAN_PLUGIN_VERSION:-0.14.0}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.21.1}"

# --- Backups (GCS + Workload Identity) -----------------------------------
GCS_BUCKET="${GCS_BUCKET:-${PROJECT_ID}-pg-backups}"
GSA_NAME="${GSA_NAME:-pg-backup}"
GSA_EMAIL="${GSA_EMAIL:-${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com}"

export PROJECT_ID REGION ZONE NAMESPACE PG_CLUSTER PG_IMAGE GCS_BUCKET GSA_EMAIL

# Render a template, substituting only our own variables (leaves any other
# `$…` in the YAML untouched).
render() {
  envsubst '$PROJECT_ID $REGION $NAMESPACE $PG_CLUSTER $PG_IMAGE $GCS_BUCKET $GSA_EMAIL' <"$1"
}

require_project() {
  if [[ -z "${PROJECT_ID}" ]]; then
    echo "ERROR: PROJECT_ID is not set and no default gcloud project is configured." >&2
    echo "Run 'gcloud config set project <id>' or export PROJECT_ID." >&2
    exit 1
  fi
}
