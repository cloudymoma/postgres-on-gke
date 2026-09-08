#!/usr/bin/env bash
# One-command proof of concept:
#   ./bin/demo.sh          # zonal GKE cluster + operator + 1-instance Postgres
#   ./bin/demo.sh status   # connection info
#   ./bin/demo.sh clean    # tear everything down
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=../config.sh
source ./config.sh
require_project

up() {
  ./bin/gke.sh create demo
  ./bin/cnpg.sh install
  ./bin/pg.sh deploy demo
  echo
  echo "==> Demo cluster is up. Connection info:"
  ./bin/pg.sh password
  echo
  echo "Try it:  ./bin/pg.sh psql"
  echo "Clean up with:  ./bin/demo.sh clean"
}

status() {
  ./bin/pg.sh status
  echo
  ./bin/pg.sh password
}

clean() {
  ./bin/gke.sh delete demo
}

case "${1:-up}" in
  up)     up ;;
  status) status ;;
  clean)  clean ;;
  *) echo "usage: $0 [up|status|clean]" >&2; exit 1 ;;
esac
