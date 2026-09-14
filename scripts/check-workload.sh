#!/usr/bin/env bash
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# != 1 ]]; then
  echo 'Select database, gateway, sandbox, cnpg, or cnpg-gateway.' >&2
  exit 2
fi
case "$1" in
  cnpg)
    : "${STEGO_TEST_CONTEXT:?Set an explicit operator context for the bounded CNPG fixture}"
    exec python3 "$project/scripts/check-cnpg-database.py" --context "$STEGO_TEST_CONTEXT"
    ;;
  database|gateway|sandbox|cnpg-gateway)
    echo "The $1 workload fixture still needs conversion to restricted jshell access." >&2
    echo 'This required check cannot pass until its replacement runs. Privileged kind nodes are prohibited.' >&2
    exit 1
    ;;
  *)
    echo 'Unknown workload check.' >&2
    exit 2
    ;;
esac
