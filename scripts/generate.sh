#!/usr/bin/env bash
set -euo pipefail

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
if [[ $# -gt 1 || (${1:-} != "" && ${1:-} != "--check") ]]; then
  echo 'Usage: scripts/generate.sh [--check]' >&2
  exit 2
fi
revision=$(cat .stego/compiler-revision)
if [[ ! $revision =~ ^[0-9a-f]{40}$ ]]; then
  echo 'Compiler revision must be a full Git commit ID.' >&2
  exit 1
fi
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
git init -q "$scratch/compiler"
git -C "$scratch/compiler" remote add origin https://github.com/jsell-rh/stego.git
git -C "$scratch/compiler" fetch -q --depth=1 origin "$revision"
git -C "$scratch/compiler" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
test "$(git -C "$scratch/compiler" rev-parse HEAD)" = "$revision"
(
  cd "$scratch/compiler"
  GOWORK=off go build -mod=readonly -trimpath -o "$scratch/stego" ./cmd/stego
)
unset STEGO_REGISTRY STEGO_MODULE STEGO_GO_VERSION
export GOWORK=off
"$scratch/stego" apply
"$scratch/stego" deps
"$scratch/stego" apply
"$scratch/stego" drift
if [[ ${1:-} == --check ]]; then
  git diff --exit-code -- out .stego/state.yaml go.mod go.sum
  if [[ -n $(git ls-files --others --exclude-standard -- out .stego/state.yaml) ]]; then
    echo 'Regeneration produced untracked output.' >&2
    exit 1
  fi
fi
