#!/usr/bin/env bash
# Expose the cluster inside the VPC via internal TCP load balancers
# (one for the primary/read-write, one for replicas/read-only).
#
#   ./bin/lb.sh deploy
#   ./bin/lb.sh status
#   ./bin/lb.sh delete
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh

deploy() {
  render templates/lb.yml | kubectl apply -f -
  echo "==> Waiting for internal IPs (up to ~2 min)..."
  kubectl -n "$NAMESPACE" wait --for=jsonpath='{.status.loadBalancer.ingress}' \
    "svc/${PG_CLUSTER}-lb-rw" --timeout 180s || true
  status
}

status() {
  kubectl -n "$NAMESPACE" get svc "${PG_CLUSTER}-lb-rw" "${PG_CLUSTER}-lb-ro" -o wide
}

delete() {
  render templates/lb.yml | kubectl delete -f - --ignore-not-found
}

case "${1:-}" in
  deploy) deploy ;;
  status) status ;;
  delete) delete ;;
  *) echo "usage: $0 {deploy|status|delete}" >&2; exit 1 ;;
esac
