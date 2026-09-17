#!/usr/bin/env bash
set -euo pipefail
umask 077

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
[[ $(cat .stego/compiler-revision) == $(cat gateway-console/.stego/compiler-revision) ]]
check=false
gateway_only=false
for argument in "$@"; do
  case "$argument" in
    --check) [[ $check == false ]]; check=true ;;
    --gateway-console-only) [[ $gateway_only == false ]]; gateway_only=true ;;
    *) echo 'Usage: scripts/generate.sh [--check] [--gateway-console-only]' >&2; exit 2 ;;
  esac
done
# Keep the source pin, checked compiler, and generation log for inspection.
record_root=${STEGO_GENERATION_ROOT:-${XDG_STATE_HOME:-$HOME/.local/state}/stego/generation}
[[ $record_root == /* ]]
mkdir -p -- "$record_root"
record=$(mktemp -d "$record_root/generation.XXXXXXXX")
if [[ -n ${STEGO_VERIFIED_COMPILER:-} ]]; then
  # The CI host has authenticated this compiler. Capture bounded bytes and use
  # the independent source pin before execution; do not trust a supplied manifest.
  [[ $STEGO_VERIFIED_COMPILER == /* && -f $STEGO_VERIFIED_COMPILER && ! -L $STEGO_VERIFIED_COMPILER ]]
  compiler="$record/stego"
  timeout --signal=TERM --kill-after=5s 15s head -c 67108865 -- "$STEGO_VERIFIED_COMPILER" > "$compiler"
  chmod 700 "$compiler"
else
  prepare_args=("$record/compiler-setup")
  if [[ -n ${STEGO_COMPILER_PACKAGE:-} ]]; then prepare_args+=("$STEGO_COMPILER_PACKAGE"); fi
  bash scripts/prepare-compiler.sh "${prepare_args[@]}"
  compiler="$record/compiler-setup/verified/stego-linux-amd64"
fi
# Signature verification uses GitHub authentication. Generation does not need it.
unset GH_TOKEN GITHUB_TOKEN STEGO_REGISTRY STEGO_MODULE STEGO_GO_VERSION
expected=$(cat .stego/compiler-sha256)
[[ $expected =~ ^[0-9a-f]{64}$ ]]
if [[ $(sha256sum "$compiler") != "$expected  $compiler" ]]; then
  echo 'Compiler bytes differ from the selected digest.' >&2
  exit 1
fi
timeout --signal=TERM --kill-after=5s 30s "$compiler" version --json > "$record/compiler-version.json"
python3 -I -B - "$record/compiler-version.json" "$project/.stego/compiler-revision" <<'COMPILER_IDENTITY'
import json,sys
from pathlib import Path
build=json.loads(Path(sys.argv[1]).read_text())['build']
if build['revision'] != Path(sys.argv[2]).read_text().strip() or build['source_state'] != 'clean':
    raise SystemExit('The compiler identity differs from the selected revision')
COMPILER_IDENTITY
export GOWORK=off
python3 scripts/check-gateway-console-inputs.py
modules=(. console gateway-console)
if [[ $gateway_only == true ]]; then modules=(gateway-console); fi
{
  for module in "${modules[@]}"; do
    (
      cd "$project/$module"
      timeout --signal=TERM --kill-after=5s 5m "$compiler" apply
      timeout --signal=TERM --kill-after=5s 5m "$compiler" deps
      timeout --signal=TERM --kill-after=5s 5m "$compiler" apply
      if [[ $module == gateway-console ]]; then
        cp .stego/state.yaml "$record/first-state.yaml"
        timeout --signal=TERM --kill-after=5s 5m "$compiler" apply
        cmp .stego/state.yaml "$record/first-state.yaml"
      fi
      timeout --signal=TERM --kill-after=5s 5m "$compiler" drift
    )
  done
} 2>&1 | tee "$record/generation.log"
if [[ $check == true ]]; then
  for module in "${modules[@]}"; do
    (
      cd "$project/$module"
      git diff --exit-code -- out .stego/state.yaml go.mod go.sum
      if [[ -n $(git ls-files --others --exclude-standard -- out .stego/state.yaml) ]]; then
        echo 'Regeneration produced untracked output.' >&2
        exit 1
      fi
    )
  done
fi
