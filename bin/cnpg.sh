#!/usr/bin/env bash
# Install / upgrade the CloudNativePG operator and the Barman Cloud plugin
# (backup engine). cert-manager is a prerequisite of the plugin.
#
#   ./bin/cnpg.sh install
#   ./bin/cnpg.sh status
#
# To upgrade: bump CNPG_VERSION / BARMAN_PLUGIN_VERSION / CERT_MANAGER_VERSION
# in config.sh and run `install` again — the manifests apply idempotently.
# NOTE: a new operator or plugin version triggers a rolling restart of every
# Postgres pod, ending in a switchover (seconds of write downtime). Upgrade
# in a quiet period.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh

install() {
  echo "==> Installing cert-manager ${CERT_MANAGER_VERSION}"
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  for d in cert-manager cert-manager-cainjector cert-manager-webhook; do
    kubectl -n cert-manager rollout status deployment "$d" --timeout 180s
  done

  echo "==> Installing CloudNativePG operator ${CNPG_VERSION}"
  kubectl apply --server-side -f \
    "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v${CNPG_VERSION}/cnpg-${CNPG_VERSION}.yaml"
  kubectl -n cnpg-system rollout status deployment cnpg-controller-manager --timeout 180s

  echo "==> Installing Barman Cloud plugin ${BARMAN_PLUGIN_VERSION}"
  # The manifest holds cert-manager Certificate/Issuer objects. Right after
  # install the cert-manager webhook can be Ready but not yet serving a trusted
  # cert, so retry briefly instead of failing the whole setup.
  local url="https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/v${BARMAN_PLUGIN_VERSION}/manifest.yaml"
  local attempt
  for attempt in 1 2 3 4 5 6; do
    kubectl apply -f "$url" && break
    if (( attempt == 6 )); then
      echo "ERROR: Barman Cloud plugin install failed after ${attempt} attempts" >&2
      exit 1
    fi
    echo "    (cert-manager webhook not ready yet; retrying in 10s)"
    sleep 10
  done
  kubectl -n cnpg-system rollout status deployment barman-cloud --timeout 180s

  echo "==> Done. Operator status:"
  status
}

status() {
  kubectl -n cnpg-system get deployments,pods
}

case "${1:-}" in
  install) install ;;
  status)  status ;;
  *) echo "usage: $0 {install|status}" >&2; exit 1 ;;
esac
