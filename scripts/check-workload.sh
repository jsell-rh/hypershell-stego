#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 1 ]]; then
  echo 'Select database, gateway, or sandbox.' >&2
  exit 2
fi
case "$1" in
  database)
    echo 'The database catalog and server controller are retired. Use the Gateway SQL checks.' >&2
    exit 1
    ;;
  gateway|sandbox)
    echo "The $1 workload CI check needs its installation fixture and restricted jshell runner." >&2
    echo 'This check remains required. A local kind cluster is not used.' >&2
    exit 1
    ;;
  *)
    echo 'Unknown workload check.' >&2
    exit 2
    ;;
esac
