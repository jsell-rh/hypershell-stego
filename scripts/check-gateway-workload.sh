#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 0 ]]; then
  echo 'This workflow command takes no arguments.' >&2
  exit 2
fi
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
exec "$project/scripts/check-workload.sh" gateway
