#!/usr/bin/env bash
set -euo pipefail
compiler_token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
unset GH_TOKEN GITHUB_TOKEN

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
suite=all
if [[ $# == 1 && ($1 == --suite=core || $1 == --suite=browser) ]]; then
  suite=${1#--suite=}
elif [[ $# != 0 ]]; then
  echo 'Usage: scripts/check-gateway.sh [--suite=core|--suite=browser]' >&2
  exit 2
fi
if [[ -z ${STEGO_TEST_POSTGRES_DSN:-} ]]; then
  echo 'Set STEGO_TEST_POSTGRES_DSN to a PostgreSQL connection that can create test databases.' >&2
  exit 1
fi
if [[ -z ${STEGO_TEST_POSTGRES_CA_FILE:-} || ! -r $STEGO_TEST_POSTGRES_CA_FILE ]]; then
  echo 'Set STEGO_TEST_POSTGRES_CA_FILE to the public CA file for the database TLS fixture.' >&2
  exit 1
fi
export STEGO_REQUIRE_POSTGRES=1
export STEGO_REQUIRE_KEYCLOAK=1
export GOWORK=off
go mod verify
GH_TOKEN="$compiler_token" scripts/generate.sh --check
unset compiler_token
case "$suite" in
  core)
    # CI runs the excluded workflow in its separate required browser job.
    # Stream each test result so failures are visible before the package ends.
    go test -json -race -count=1 -mod=readonly -timeout=30m \
      -skip '^TestGeneratedBrowserGatewayWorkflow$' ./...
    ;;
  browser)
    export STEGO_REQUIRE_BROWSER=1
    test_output=$(mktemp)
    trap 'rm -f -- "$test_output"' EXIT
    go test -json -race -count=1 -mod=readonly -timeout=8m ./acceptance \
      -run '^TestGeneratedBrowserGatewayWorkflow$' | tee "$test_output"
    python3 - "$test_output" <<'PY'
import json
import sys
from pathlib import Path
events = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
if not any(event.get('Action') == 'pass' and event.get('Test') == 'TestGeneratedBrowserGatewayWorkflow' for event in events):
    raise SystemExit('The rendered browser workflow has no passing result')
PY
    ;;
  all)
    go test -v -race -count=1 -mod=readonly -timeout=25m ./...
    ;;
esac
