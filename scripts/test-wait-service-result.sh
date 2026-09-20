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
STEGO_TEST_CONTEXT=unused-mock
oc_cmd=(bash "$fixture/oc-mock")
sleep() { :; }
python3() {
  if [[ ${1:-} == "$project/scripts/collect-service-startup.py" ]]; then
    test "$*" = "$project/scripts/collect-service-startup.py --context unused-mock --namespace fixture --output $results/terminal-pods.json"
    if [[ $scenario == status-unavailable ]]; then
      printf '{"complete":false,"error_type":"TimeoutExpired"}\n' > "$results/terminal-pods.json"
      return 1
    fi
    printf '{"complete":true,"pods":[]}\n' > "$results/terminal-pods.json"
    return 0
  fi
  if [[ ${1:-} == "$project/scripts/change-gateway-endpoint.py" ]]; then
    local count
    count=$(cat "$results/transition-attempts")
    count=$((count + 1))
    printf '%s\n' "$count" > "$results/transition-attempts"
    if ((count == 1)); then return 75; fi
    return 0
  fi
  command python3 "$@"
}
cat > "$fixture/oc-mock" <<'SHEND'
set -euo pipefail
  case "$*" in
    *"cat /work/deployment.exit")
      n=$(cat "$results/attempts")
      n=$((n + 1))
      printf '%s\n' "$n" > "$results/attempts"
      case "$scenario" in
        failed|missing|succeeded|status-unavailable) exit 1 ;;
      esac
      if ((n == 1)); then
        case "$scenario" in
          empty) exit 0 ;;
          transient|endpoint-retry) exit 1 ;;
          malformed) printf 'private-invalid-record'; exit 0 ;;
        esac
      fi
      printf '42\n'
      ;;
    *"get pod"*)
      case "$scenario" in
        failed|status-unavailable) printf 'Failed' ;;
        succeeded) printf 'Succeeded' ;;
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
for scenario in empty transient malformed failed missing succeeded status-unavailable endpoint-retry; do
  results="$fixture/$scenario"
  mkdir "$results"
  printf '0\n' > "$results/attempts"
  endpoint_change=0
  if [[ $scenario == endpoint-retry ]]; then
    endpoint_change=1
    STEGO_TEST_CONTEXT=unused-mock
    printf '0\n' > "$results/transition-attempts"
  fi
  result=
  status=0
  wait_service_result > "$results/observations" 2>&1 || status=$?
  case "$scenario" in
    failed|missing|succeeded|status-unavailable)
      test "$status" = 1
      test "$(cat "$results/deployment.log")" = 'retained test output'
      test -s "$results/terminal-pods.json"
      if [[ $scenario == status-unavailable ]]; then
        test "$(cat "$results/terminal-pods.json")" = '{"complete":false,"error_type":"TimeoutExpired"}'
      fi
      ;;
    *)
      test "$status" = 0
      test "$result" = 42
      test "$(cat "$results/attempts")" = 2
      test ! -s "$results/observations"
      test ! -e "$results/terminal-pods.json"
      ;;
  esac
  if [[ $scenario == endpoint-retry ]]; then test "$(cat "$results/transition-attempts")" = 2; fi
done
printf 'Service completion protocol passed eight cases.\n'
