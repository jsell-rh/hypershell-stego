#!/usr/bin/env bash
# Fetch the selected STEGO tooling and use its common signature policy.
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$project/scripts/check-phases.sh"
check_phases_init compiler-setup
if [[ $# -lt 1 || $# -gt 2 || $1 != /* ]]; then
  echo 'Usage: scripts/prepare-compiler.sh NEW_ABSOLUTE_DIRECTORY [PACKAGE_DIRECTORY]' >&2
  exit 2
fi
record=$1
revision=$(cat "$project/.stego/compiler-revision")
tooling=$(cat "$project/.stego/tooling-revision")
expected=$(cat "$project/.stego/compiler-sha256")
[[ $expected =~ ^[0-9a-f]{64}$ ]]
if [[ ! $revision =~ ^[0-9a-f]{40}$ || ! $tooling =~ ^[0-9a-f]{40}$ ]]; then
  echo 'Compiler and tooling revisions must be full lowercase commit IDs.' >&2
  exit 1
fi
if [[ $(cat "$project/gateway-console/.stego/compiler-revision") != "$revision" ]]; then
  echo 'All modules must select the same compiler revision.' >&2
  exit 1
fi
# The source pin is the bootstrap trust anchor. Download and signature handling
# stay in STEGO. No compiler source build is permitted as a fallback.
check_phase_start tooling-init
mkdir -m 700 -- "$record"
mkdir -m 700 -- "$record/home"
git_command=(env -i PATH=/usr/bin:/bin HOME="$record/home" LANG=C
  GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
  timeout --signal=TERM --kill-after=5s 120s git)
"${git_command[@]}" init -q "$record/tooling"
"${git_command[@]}" -C "$record/tooling" remote add origin https://github.com/jsell-rh/stego.git
check_phase_done
check_phase_start tooling-fetch
"${git_command[@]}" -C "$record/tooling" fetch -q --depth=1 origin "$tooling"
check_phase_done
check_phase_start tooling-checkout
"${git_command[@]}" -C "$record/tooling" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
test "$("${git_command[@]}" -C "$record/tooling" rev-parse HEAD)" = "$tooling"
check_phase_done
check_phase_start compiler-install
source_args=(--release)
if [[ $# == 2 ]]; then source_args=(--package "$2"); fi
timeout --signal=TERM --kill-after=5s 12m python3 -I -B \
  "$record/tooling/scripts/install-compiler.py" "${source_args[@]}" \
  --revision "$revision" --gh "$(command -v gh)" --output "$record/verified" \
  > "$record/installation.json"
check_phase_done
check_phase_start compiler-digest
[[ $(sha256sum "$record/verified/stego-linux-amd64") == "$expected  $record/verified/stego-linux-amd64" ]]
printf '%s\n' "$tooling" > "$record/tooling-revision"
printf '%s\n' "$revision" > "$record/compiler-revision"
check_phase_done
