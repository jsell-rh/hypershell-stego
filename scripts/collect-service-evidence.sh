#!/usr/bin/env bash
# Source from the frozen service wrapper after a valid completion record.
collect_service_evidence() {
  local status=0 public=0 transfer_status=0
  if [[ -n ${STEGO_TEST_GATEWAY_PUBLIC_CONFIG:-} || ${STEGO_TEST_REQUIRE_PUBLIC_GATEWAY:-0} == 1 ]]; then public=1; fi
  timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- cat /work/deployment.log > "$results/deployment.log" || {
    transfer_status=$?
    printf 'Service log transfer failed with exit status %s.\n' "$transfer_status" >&2
    status=1
  }
  timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- sh -c '
    cd /work || exit 1
    evidence_status=0
    require_evidence() {
      if [ ! -s "$1" ]; then
        printf "Required service evidence is missing or empty: %s\n" "$1" >&2
        evidence_status=1
      fi
    }
    if [ "$1" = 0 ]; then
      for file in deployment.exit first.sha256 second.sha256 after-tests.sha256 generated.tar compiler-transfer.json compiler/build.json compiler/verified.json compiler/provenance.jsonl compiler/SHA256SUMS; do
        require_evidence "$file"
      done
      if [ "$2" = 1 ]; then
        for file in image-delivery-transfer.json image-publication/source-check.json image-publication/publication.json image-delivery/compiler/build.json image-delivery/compiler/verified.json image-delivery/compiler/provenance.jsonl image-delivery/compiler/SHA256SUMS browser-artifacts/verify.json browser-artifacts/verify.json.png browser-artifacts/browser-startup-signals.json; do
          require_evidence "$file"
        done
        for image in hypershell hypershell-console hypershell-provisioner hypershell-gateway-console hypershell-namespace-allocation hypershell-gateway-identity hypershell-gateway-workload; do
          require_evidence "image-publication/$image/registry.json"
        done
      else
        require_evidence image.json
        require_evidence worker-image.json
      fi
      if [ "$3" = 1 ]; then
        for file in browser-artifacts/gateway-cleanup-timing.json browser-artifacts/allocation-finalization.json browser-artifacts/postgres-server.json browser-artifacts/gateway-network-initial.json browser-artifacts/gateway-network-after-recovery.json; do
          require_evidence "$file"
        done
      fi
      if [ "$4" = 1 ]; then
        for file in browser-artifacts/gateway-public-rpc.json browser-artifacts/gateway-public-network-recovery.json browser-artifacts/gateway-public-certificate-rotation.json browser-artifacts/provisioner-restart.json; do
          require_evidence "$file"
        done
      fi
      if [ "$5" = 1 ]; then
        for file in browser-artifacts/gateway-network-after-endpoint-replacement.json network-endpoint-change.request network-endpoint-change.ack; do
          require_evidence "$file"
        done
      fi
    fi
    set --
    for file in compiler-transfer.json compiler/build.json compiler/verified.json compiler/provenance.jsonl compiler/SHA256SUMS deployment.exit image.json console-image.json gateway-console-image.json worker-image.json provisioner-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts network-endpoint-change.request network-endpoint-change.ack; do
      if [ -e "$file" ]; then set -- "$@" "$file"; fi
    done
    # Select only public records. A stopped publisher can leave private files.
    for file in image-delivery-transfer.json image-publication/source-check.json image-publication/publication.json image-delivery/compiler/build.json image-delivery/compiler/verified.json image-delivery/compiler/provenance.jsonl image-delivery/compiler/SHA256SUMS; do
      if [ -e "$file" ]; then set -- "$@" "$file"; fi
    done
    for image in hypershell hypershell-console hypershell-provisioner hypershell-gateway-console hypershell-namespace-allocation hypershell-gateway-identity hypershell-gateway-workload; do
      if [ -e "image-publication/$image/registry.json" ]; then set -- "$@" "image-publication/$image/registry.json"; fi
    done
    # Keep available evidence even when a required file is absent.
    tar cf - "$@" || exit 1
    exit "$evidence_status"
  ' stego-collect "$result" "${STEGO_TEST_BROWSER_DEPLOYMENT:-0}" "${workload:-0}" "$public" "${endpoint_change:-0}" > "$results/evidence.tar" || {
    transfer_status=$?
    printf 'Service archive collection failed with exit status %s.\n' "$transfer_status" >&2
    status=1
  }
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
