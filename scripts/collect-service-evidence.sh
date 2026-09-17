#!/usr/bin/env bash
# Source from the frozen service wrapper after a valid completion record.
collect_service_evidence() {
  local status=0 public=0
  if [[ -n ${STEGO_TEST_GATEWAY_PUBLIC_CONFIG:-} || ${STEGO_TEST_REQUIRE_PUBLIC_GATEWAY:-0} == 1 ]]; then public=1; fi
  timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- cat /work/deployment.log > "$results/deployment.log" || status=1
  timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- sh -c '
    cd /work || exit 1
    if [ "$1" = 0 ]; then
      for file in deployment.exit image.json first.sha256 second.sha256 after-tests.sha256 generated.tar; do
        [ -s "$file" ] || exit 1
      done
      if [ "$2" = 1 ]; then
        for file in console-image.json provisioner-image.json browser-artifacts/verify.json browser-artifacts/verify.json.png browser-artifacts/browser-startup-signals.json; do
          [ -s "$file" ] || exit 1
        done
      else
        [ -s worker-image.json ] || exit 1
      fi
      if [ "$3" = 1 ]; then
        for file in gateway-console-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json browser-artifacts/postgres-server.json browser-artifacts/gateway-network-initial.json browser-artifacts/gateway-network-after-recovery.json; do
          [ -s "$file" ] || exit 1
        done
      fi
      if [ "$4" = 1 ]; then
        for file in browser-artifacts/gateway-public-rpc.json browser-artifacts/gateway-public-network-recovery.json browser-artifacts/gateway-public-certificate-rotation.json browser-artifacts/provisioner-restart.json; do
          [ -s "$file" ] || exit 1
        done
      fi
      if [ "$5" = 1 ]; then
        for file in browser-artifacts/gateway-network-after-endpoint-replacement.json network-endpoint-change.request network-endpoint-change.ack; do
          [ -s "$file" ] || exit 1
        done
      fi
    fi
    set --
    for file in deployment.exit image.json console-image.json gateway-console-image.json worker-image.json provisioner-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts network-endpoint-change.request network-endpoint-change.ack; do
      if [ -e "$file" ]; then set -- "$@" "$file"; fi
    done
    tar cf - "$@"
  ' stego-collect "$result" "${STEGO_TEST_BROWSER_DEPLOYMENT:-0}" "${workload:-0}" "$public" "${endpoint_change:-0}" > "$results/evidence.tar" || status=1
  if [[ $result == 0 && ${endpoint_change:-0} == 1 ]]; then
    python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); assert r["phase"]=="complete" and r["type_checks"]=="passed"' \
      "$results/endpoint-change/journal.json" || status=1
  fi
  if [[ ! -s $results/deployment.log || ! -s $results/evidence.tar ]]; then status=1; fi
  timeout --signal=TERM --kill-after=5s 45s tar tf "$results/evidence.tar" >/dev/null 2>&1 || status=1
  if ((status != 0)); then
    echo 'Service evidence collection failed. The run cannot pass.' >&2
  fi
  return "$status"
}
