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
for scenario in service browser workload public configured-public log-failure archive-failure truncated missing-compiler-transfer missing-compiler-signatures missing-image missing-gateway-console-image missing-regeneration missing-screen missing-startup missing-sql missing-cleanup-timing empty-cleanup-timing missing-allocation-finalization empty-allocation-finalization missing-network missing-public missing-provisioner-restart missing-multiple empty-provisioner-restart failed-test endpoint missing-endpoint missing-endpoint-ack unfinished-endpoint; do
  test_work="$fixture/$scenario/work"
  results="$fixture/$scenario/results"
  mkdir -p "$test_work/browser-artifacts" "$test_work/compiler" "$results"
  for file in compiler-transfer.json compiler/build.json compiler/verified.json compiler/provenance.jsonl compiler/SHA256SUMS deployment.exit image.json worker-image.json console-image.json gateway-console-image.json provisioner-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts/verify.json browser-artifacts/verify.json.png browser-artifacts/browser-startup-signals.json browser-artifacts/gateway-cleanup-timing.json browser-artifacts/allocation-finalization.json browser-artifacts/postgres-server.json browser-artifacts/gateway-network-initial.json browser-artifacts/gateway-network-after-recovery.json browser-artifacts/gateway-public-rpc.json browser-artifacts/gateway-public-network-recovery.json browser-artifacts/gateway-public-certificate-rotation.json browser-artifacts/provisioner-restart.json; do
    printf 'record\n' > "$test_work/$file"
  done
  result=0
  workload=1
  STEGO_TEST_BROWSER_DEPLOYMENT=1
  STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=1
  STEGO_TEST_GATEWAY_PUBLIC_CONFIG=
  expected=0
  endpoint_change=0
  case "$scenario" in
    endpoint|missing-endpoint|missing-endpoint-ack|unfinished-endpoint)
      endpoint_change=1
      mkdir "$results/endpoint-change"
      printf '%s\n' '{"phase":"complete","type_checks":"passed"}' > "$results/endpoint-change/journal.json"
      for file in browser-artifacts/gateway-network-after-endpoint-replacement.json network-endpoint-change.request network-endpoint-change.ack; do
        printf 'record\n' > "$test_work/$file"
      done
      ;;
  esac
  case "$scenario" in
    service) workload=0; STEGO_TEST_BROWSER_DEPLOYMENT=0; STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm -rf -- "$test_work/browser-artifacts" ;;
    browser) workload=0; STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm "$test_work/browser-artifacts/postgres-server.json" "$test_work/browser-artifacts/gateway-public-rpc.json" ;;
    workload) STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; rm "$test_work/browser-artifacts/gateway-public-rpc.json" ;;
    configured-public) STEGO_TEST_REQUIRE_PUBLIC_GATEWAY=0; STEGO_TEST_GATEWAY_PUBLIC_CONFIG=configured.json ;;
    log-failure|archive-failure|truncated) expected=1 ;;
    missing-compiler-transfer) expected=1; rm "$test_work/compiler-transfer.json" ;;
    missing-compiler-signatures) expected=1; rm "$test_work/compiler/provenance.jsonl" ;;
    missing-image) expected=1; rm "$test_work/image.json" ;;
    missing-gateway-console-image) expected=1; rm "$test_work/gateway-console-image.json" ;;
    missing-regeneration) expected=1; rm "$test_work/after-tests.sha256" ;;
    missing-screen) expected=1; rm "$test_work/browser-artifacts/verify.json.png" ;;
    missing-startup) expected=1; rm "$test_work/browser-artifacts/browser-startup-signals.json" ;;
    missing-sql) expected=1; rm "$test_work/browser-artifacts/postgres-server.json" ;;
    missing-cleanup-timing) expected=1; rm "$test_work/browser-artifacts/gateway-cleanup-timing.json" ;;
    missing-allocation-finalization) expected=1; rm "$test_work/browser-artifacts/allocation-finalization.json" ;;
    empty-allocation-finalization) expected=1; : > "$test_work/browser-artifacts/allocation-finalization.json" ;;
    empty-cleanup-timing) expected=1; : > "$test_work/browser-artifacts/gateway-cleanup-timing.json" ;;
    missing-network) expected=1; rm "$test_work/browser-artifacts/gateway-network-after-recovery.json" ;;
    missing-provisioner-restart) expected=1; rm "$test_work/browser-artifacts/provisioner-restart.json" ;;
    empty-provisioner-restart) expected=1; : > "$test_work/browser-artifacts/provisioner-restart.json" ;;
    missing-multiple) expected=1; rm "$test_work/image.json" "$test_work/browser-artifacts/provisioner-restart.json" ;;
    missing-public) expected=1; rm "$test_work/browser-artifacts/gateway-public-certificate-rotation.json" ;;
    missing-endpoint) expected=1; rm "$test_work/browser-artifacts/gateway-network-after-endpoint-replacement.json" ;;
    missing-endpoint-ack) expected=1; rm "$test_work/network-endpoint-change.ack" ;;
    unfinished-endpoint) expected=1; printf '%s\n' '{"phase":"checking","type_checks":"pending"}' > "$results/endpoint-change/journal.json" ;;
    failed-test) result=42; rm "$test_work/image.json" "$test_work/after-tests.sha256"; rm -rf -- "$test_work/browser-artifacts" ;;
  esac
  observed=0
  collect_service_evidence > "$results/collector.log" 2>&1 || observed=$?
  if [[ $observed != "$expected" ]]; then
    printf 'Evidence collection case failed: %s\n' "$scenario" >&2
    exit 1
  fi
  case "$scenario" in
    missing-*|empty-provisioner-restart|empty-cleanup-timing|empty-allocation-finalization)
      # An incomplete record must fail and retain all available evidence.
      tar tf "$results/evidence.tar" >/dev/null
      cmp "$test_work/generated.tar" <(tar xOf "$results/evidence.tar" generated.tar)
      for file in image.json gateway-console-image.json after-tests.sha256 browser-artifacts/verify.json.png browser-artifacts/browser-startup-signals.json browser-artifacts/gateway-cleanup-timing.json browser-artifacts/allocation-finalization.json browser-artifacts/postgres-server.json browser-artifacts/gateway-network-after-recovery.json browser-artifacts/gateway-public-certificate-rotation.json browser-artifacts/provisioner-restart.json; do
        if [[ ! -s $test_work/$file ]]; then
          grep -Fqx "Required service evidence is missing or empty: $file" "$results/collector.log"
        fi
      done
      if [[ $scenario == missing-endpoint || $scenario == missing-endpoint-ack ]]; then
        grep -Fq 'Required service evidence is missing or empty:' "$results/collector.log"
      fi
      ;;
  esac
  if [[ $observed == 0 ]]; then
    tar tf "$results/evidence.tar" >/dev/null
    if [[ $workload == 1 && $result == 0 ]]; then
      cmp "$test_work/gateway-console-image.json" <(tar xOf "$results/evidence.tar" gateway-console-image.json)
    fi
  fi
done
printf 'Service evidence collection cases passed.\n'
