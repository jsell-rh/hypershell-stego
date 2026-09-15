#!/usr/bin/env bash
# Check collection failures with local files and a fake oc command.
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$project/scripts/collect-service-evidence.sh"
fixture=$(mktemp -d "${TMPDIR:-/tmp}/stego-evidence-test.XXXXXXXX")
trap 'rm -rf -- "$fixture"' EXIT
namespace=fixture
pod=fixture
oc_cmd=(bash "$fixture/oc-mock")
cat > "$fixture/oc-mock" <<'MOCK'
set -euo pipefail
case "$*" in
  *"cat /work/deployment.log")
    if [[ $scenario == log-failure ]]; then exit 1; fi
    printf 'test output\n'
    ;;
  *)
    while [[ $1 != sh ]]; do shift; done
    shift
    [[ $1 == -c ]]
    shift
    script=$1
    shift
    if [[ $scenario == archive-failure ]]; then printf 'partial'; exit 1; fi
    if [[ $scenario == truncated ]]; then printf 'partial'; exit 0; fi
    script=${script//\/work/$test_work}
    sh -c "$script" "$@"
    ;;
esac
MOCK
export scenario test_work
for scenario in service browser workload public configured-public log-failure archive-failure truncated missing-image missing-regeneration missing-screen missing-sql missing-network missing-public failed-test; do
  test_work="$fixture/$scenario/work"
  results="$fixture/$scenario/results"
  mkdir -p "$test_work/browser-artifacts" "$results"
  for file in deployment.exit image.json worker-image.json console-image.json provisioner-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts/verify.json browser-artifacts/verify.json.png browser-artifacts/postgres-server.json browser-artifacts/gateway-network-initial.json browser-artifacts/gateway-network-after-recovery.json browser-artifacts/gateway-public-rpc.json browser-artifacts/gateway-public-network-recovery.json browser-artifacts/gateway-public-certificate-rotation.json; do
    printf 'record\n' > "$test_work/$file"
  done
  result=0
  workload=1
  STEGO_TEST_BROWSER_DEPLOYMENT=1
  STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=1
  STEGO_TEST_GATEWAY_PUBLIC_CONFIG=
  expected=0
  case "$scenario" in
    service) workload=0; STEGO_TEST_BROWSER_DEPLOYMENT=0; STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm -rf -- "$test_work/browser-artifacts" ;;
    browser) workload=0; STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm "$test_work/browser-artifacts/postgres-server.json" "$test_work/browser-artifacts/gateway-public-rpc.json" ;;
    workload) STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm "$test_work/browser-artifacts/gateway-public-rpc.json" ;;
    configured-public) STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; STEGO_TEST_GATEWAY_PUBLIC_CONFIG=configured.json ;;
    log-failure|archive-failure|truncated) expected=1 ;;
    missing-image) expected=1; rm "$test_work/image.json" ;;
    missing-regeneration) expected=1; rm "$test_work/after-tests.sha256" ;;
    missing-screen) expected=1; rm "$test_work/browser-artifacts/verify.json.png" ;;
    missing-sql) expected=1; rm "$test_work/browser-artifacts/postgres-server.json" ;;
    missing-network) expected=1; rm "$test_work/browser-artifacts/gateway-network-after-recovery.json" ;;
    missing-public) expected=1; rm "$test_work/browser-artifacts/gateway-public-certificate-rotation.json" ;;
    failed-test) result=42; rm "$test_work/image.json" "$test_work/after-tests.sha256"; rm -rf -- "$test_work/browser-artifacts" ;;
  esac
  observed=0
  collect_service_evidence > "$results/collector.log" 2>&1 || observed=$?
  if [[ $observed != "$expected" ]]; then
    printf 'Evidence collection case failed: %s\n' "$scenario" >&2
    exit 1
  fi
  if [[ $observed == 0 ]]; then tar tf "$results/evidence.tar" >/dev/null; fi
done
printf 'Service evidence collection passed 15 cases.\n'
