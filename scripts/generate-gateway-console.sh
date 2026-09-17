#!/usr/bin/env bash
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# -gt 1 || (${1:-} != "" && ${1:-} != --check) ]]; then
  echo 'Usage: scripts/generate-gateway-console.sh [--check]' >&2
  exit 2
fi
exec "$project/scripts/generate.sh" --gateway-console-only "$@"
