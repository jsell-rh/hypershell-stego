#!/usr/bin/env bash
# Small completion-protocol checks. This command does not call a cluster.
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$project/scripts/wait-service-result.sh"
fixture=$(mktemp -d "${TMPDIR:-/tmp}/stego-result-test.XXXXXXXX")
trap 'python3 - "$fixture" <<'PYEND'
from pathlib import Path
import shutil,sys
p=Path(sys.argv[1])
if p.name.startswith("stego-result-test."):shutil.rmtree(p)
PYEND' EXIT
namespace=fixture
pod=fixture
oc_cmd=(bash "$fixture/oc-mock")
sleep() { :; }
cat > "$fixture/oc-mock" <<'SHEND'
set -euo pipefail
  case "$*" in
    *"cat /work/deployment.exit")
      n=$(cat "$results/attempts")
      n=$((n + 1))
      printf '%s\n' "$n" > "$results/attempts"
      case "$scenario" in
        failed|missing) exit 1 ;;
      esac
      if ((n == 1)); then
        case "$scenario" in
          empty) exit 0 ;;
          transient) exit 1 ;;
          malformed) printf 'private-invalid-record'; exit 0 ;;
        esac
      fi
      printf '42\n'
      ;;
    *"get pod"*)
      case "$scenario" in
        failed) printf 'Failed' ;;
        missing) exit 0 ;;
        transient) exit 1 ;;
        *) printf 'Running' ;;
      esac
      ;;
    *"logs"*) printf 'retained test output\n' ;;
    *) exit 2 ;;
  esac
SHEND
export results scenario
for scenario in empty transient malformed failed missing; do
  results="$fixture/$scenario"
  mkdir "$results"
  printf '0\n' > "$results/attempts"
  result=
  status=0
  wait_service_result > "$results/observations" 2>&1 || status=$?
  case "$scenario" in
    failed|missing)
      test "$status" = 1
      test "$(cat "$results/deployment.log")" = 'retained test output'
      ;;
    *)
      test "$status" = 0
      test "$result" = 42
      test "$(cat "$results/attempts")" = 2
      test ! -s "$results/observations"
      ;;
  esac
done
printf 'Service completion protocol passed five cases.\n'
