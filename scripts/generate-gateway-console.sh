#!/usr/bin/env bash
set -euo pipefail

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# -gt 1 || (${1:-} != "" && ${1:-} != --check) ]]; then
  echo 'Usage: scripts/generate-gateway-console.sh [--check]' >&2
  exit 2
fi
revision=$(cat "$project/gateway-console/.stego/compiler-revision")
[[ $revision =~ ^[0-9a-f]{40}$ ]]
# Retain the exact compiler source and generation log for inspection.
record_root=${STEGO_GENERATION_ROOT:-${XDG_STATE_HOME:-$HOME/.local/state}/stego/generation}
[[ $record_root == /* ]]
mkdir -p "$record_root"
record=$(mktemp -d "$record_root/gateway-console.XXXXXXXX")
git init -q "$record/compiler"
git -C "$record/compiler" remote add origin https://github.com/jsell-rh/stego.git
git -C "$record/compiler" fetch -q --depth=1 origin "$revision"
git -C "$record/compiler" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
test "$(git -C "$record/compiler" rev-parse HEAD)" = "$revision"
(
  cd "$record/compiler"
  GOWORK=off go build -mod=readonly -trimpath -buildvcs=true -o "$record/stego" ./cmd/stego
)
unset STEGO_REGISTRY STEGO_MODULE STEGO_GO_VERSION
export GOWORK=off
cd "$project/gateway-console"
python3 "$project/scripts/check-gateway-console-inputs.py"
{
  "$record/stego" apply
  "$record/stego" deps
  "$record/stego" apply
  cp .stego/state.yaml "$record/first-state.yaml"
  "$record/stego" apply
  cmp .stego/state.yaml "$record/first-state.yaml"
  "$record/stego" drift
} 2>&1 | tee "$record/generation.log"
if [[ ${1:-} == --check ]]; then
  git -C "$project" diff --exit-code -- gateway-console/out gateway-console/.stego/state.yaml gateway-console/go.mod gateway-console/go.sum
  if [[ -n $(git -C "$project" ls-files --others --exclude-standard -- gateway-console/out gateway-console/.stego/state.yaml) ]]; then
    echo 'Gateway console generation produced untracked output.' >&2
    exit 1
  fi
fi
