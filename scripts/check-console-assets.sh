#!/usr/bin/env bash
set -euo pipefail
umask 077

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
if [[ -n ${STEGO_BIN:-} ]]; then
  echo 'Use STEGO_COMPILER_PACKAGE to supply a compiler package for signature verification.' >&2
  exit 2
fi
record_root=${STEGO_ASSET_RECORD_ROOT:-${XDG_STATE_HOME:-$HOME/.local/state}/stego/assets}
[[ $record_root == /* ]]
mkdir -p -- "$record_root"
output=$(mktemp -d "$record_root/check.XXXXXXXX")
prepare_args=("$output/compiler-setup")
if [[ -n ${STEGO_COMPILER_PACKAGE:-} ]]; then prepare_args+=("$STEGO_COMPILER_PACKAGE"); fi
bash scripts/prepare-compiler.sh "${prepare_args[@]}"
unset GH_TOKEN GITHUB_TOKEN STEGO_REGISTRY STEGO_MODULE STEGO_GO_VERSION
compiler="$output/compiler-setup/verified/stego-linux-amd64"
timeout --signal=TERM --kill-after=5s 30s "$compiler" assets \
  --directory components/web-console/build/client --output "$output/build.zip" \
  > "$output/assets.log" 2>&1
cmp console/ui/build.zip "$output/build.zip"
