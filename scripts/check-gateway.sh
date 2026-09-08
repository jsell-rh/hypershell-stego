#!/usr/bin/env bash
set -euo pipefail

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
if [[ $# != 0 ]]; then
  echo 'Usage: scripts/check-gateway.sh' >&2
  exit 2
fi
if [[ -z ${STEGO_TEST_POSTGRES_DSN:-} ]]; then
  echo 'Set STEGO_TEST_POSTGRES_DSN to a PostgreSQL connection that can create test databases.' >&2
  exit 1
fi
export STEGO_REQUIRE_POSTGRES=1
export GOWORK=off
go mod verify
scripts/generate.sh --check
go test -race -count=1 -mod=readonly ./...
