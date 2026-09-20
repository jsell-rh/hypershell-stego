#!/bin/sh
# Run only inside the bounded browser deployment Job.
set -eu
cd /work/application
# Keep the compiler registry cache on the writable test volume.
export XDG_CACHE_HOME=/work/cache
# The host authenticated and compared these bytes before it started this Pod.
(cd /work/compiler && sha256sum --check SHA256SUMS)
compiler=/work/compiler/stego-linux-amd64

for pass in first second; do
 STEGO_VERIFIED_COMPILER="$compiler" STEGO_GENERATION_ROOT=/work/generation bash scripts/generate.sh
 find out console/out gateway-console/out -type f -print > /work/generated-files
 printf '%s\n' .stego/state.yaml go.mod go.sum console/.stego/state.yaml console/go.mod console/go.sum gateway-console/.stego/state.yaml gateway-console/.stego/compiler-revision gateway-console/go.mod gateway-console/go.sum >> /work/generated-files
 sort -o /work/generated-files /work/generated-files
 xargs sha256sum < /work/generated-files > "/work/$pass.sha256"
done
cmp /work/first.sha256 /work/second.sha256
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum console/out console/.stego/state.yaml console/go.mod console/go.sum gateway-console/out gateway-console/.stego/state.yaml gateway-console/.stego/compiler-revision gateway-console/go.mod gateway-console/go.sum
# Generation is complete. Remove only its three known lock files before the
# common compiler compares the complete source with each signed build record.
rm -f .stego/apply.lock console/.stego/apply.lock gateway-console/.stego/apply.lock
registry=image-registry.openshift-image-registry.svc:5000
registry_ca_sha256=$(sha256sum /work/registry-ca.crt | cut -d ' ' -f 1)
python3 -I -B /work/image-delivery/tools/application-images.py publish \
  --images /work/image-delivery/images --set-sha256 "$(cat /work/image-delivery/set-sha256)" \
  --source /work/application --compiler /work/image-delivery/compiler/stego-linux-amd64 \
  --compiler-sha256 "$(cat /work/image-delivery/compiler-sha256)" \
  --repository "$registry/$STEGO_TEST_NAMESPACE" --registry-ca /work/registry-ca.crt \
  --registry-ca-sha256 "$registry_ca_sha256" --username serviceaccount \
  --token-file /var/run/secrets/kubernetes.io/serviceaccount/token --output /work/image-publication
image_reference() {
  python3 -I - /work/image-publication/publication.json "$1" <<'IMAGE_REFERENCE'
import json,sys
from pathlib import Path
print(json.loads(Path(sys.argv[1]).read_text())['images'][sys.argv[2]])
IMAGE_REFERENCE
}
STEGO_TEST_SERVICE_IMAGE="$(image_reference hypershell)"
STEGO_TEST_CONSOLE_IMAGE="$(image_reference hypershell-console)"
STEGO_TEST_PROVISIONER_IMAGE="$(image_reference hypershell-provisioner)"
STEGO_TEST_GATEWAY_CONSOLE_IMAGE="$(image_reference hypershell-gateway-console)"
STEGO_TEST_ALLOCATION_WORKER_IMAGE="$(image_reference hypershell-namespace-allocation)"
STEGO_TEST_IDENTITY_WORKER_IMAGE="$(image_reference hypershell-gateway-identity)"
STEGO_TEST_GATEWAY_WORKER_IMAGE="$(image_reference hypershell-gateway-workload)"
export STEGO_TEST_SERVICE_IMAGE STEGO_TEST_CONSOLE_IMAGE STEGO_TEST_PROVISIONER_IMAGE STEGO_TEST_GATEWAY_CONSOLE_IMAGE STEGO_TEST_ALLOCATION_WORKER_IMAGE STEGO_TEST_IDENTITY_WORKER_IMAGE STEGO_TEST_GATEWAY_WORKER_IMAGE
node /work/node/npm/bin/npm-cli.js --cache /work/npm-cache ci --prefix acceptance/typescript --install-links --ignore-scripts --no-audit --no-fund
go test -race -mod=readonly -count=1 -timeout=3m ./contracts -run '^(TestGeneratedProjectInputManifest|TestConsoleDeploymentIsolation)$'
go test -race -mod=readonly -count=1 -timeout=3m ./internal/gatewayworkload ./internal/namespaceallocation ./internal/namespaceallocationapp
go test -v -race -mod=readonly -count=1 -timeout=5m -run '^(TestGatewayConsoleObservationCommitsWithWorkload|TestGatewayDeletionBeforeWorkloadStartup|TestGeneratedWorkloadWorkerStartupPrivacy|TestControllerLocal.*|TestGatewaySQLCleanupObservationIsAtomicAndSurvivesRestart|TestClusterDeletionWaitsForGatewaySQLAndWorkloadCleanupAcrossRestart|TestKubernetesWriteFailurePrivacy)$' ./acceptance
export STEGO_TEST_OC=/work/oc
export STEGO_BROWSER_ARTIFACT_DIR=/work/browser-artifacts
go test -v -race -mod=readonly -count=1 -timeout=15m -run '^TestGeneratedKubernetesBrowserGatewayWorkflow$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
(cd /work/compiler && sha256sum --check SHA256SUMS)
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum console/out console/.stego/state.yaml console/go.mod console/go.sum gateway-console/out gateway-console/.stego/state.yaml gateway-console/.stego/compiler-revision gateway-console/go.mod gateway-console/go.sum
