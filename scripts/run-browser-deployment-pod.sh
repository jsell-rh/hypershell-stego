#!/bin/sh
# Run only inside the bounded browser deployment Job.
set -eu
trap '[ ! -e /work/registry-auth.json ] || unlink /work/registry-auth.json' EXIT
cd /work/application
revision=$(cat .stego/compiler-revision)
git init -q /work/compiler
git -C /work/compiler remote add origin https://github.com/jsell-rh/stego.git
git -C /work/compiler fetch -q --depth=1 origin "$revision"
git -C /work/compiler -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
test "$(git -C /work/compiler rev-parse HEAD)" = "$revision"
(cd /work/compiler && go build -mod=readonly -trimpath -buildvcs=true -o /work/stego ./cmd/stego)
for pass in first second; do
 for target in . console; do
  (cd "$target"; /work/stego apply; /work/stego deps; /work/stego apply; /work/stego drift)
 done
 find out console/out -type f -print > /work/generated-files
 printf '%s\n' .stego/state.yaml go.mod go.sum console/.stego/state.yaml console/go.mod console/go.sum >> /work/generated-files
 sort -o /work/generated-files /work/generated-files
 xargs sha256sum < /work/generated-files > "/work/$pass.sha256"
done
cmp /work/first.sha256 /work/second.sha256
node /work/node/npm/bin/npm-cli.js --cache /work/npm-cache ci --prefix acceptance/typescript --install-links --ignore-scripts --no-audit --no-fund
go test -race -mod=readonly -count=1 -timeout=3m ./contracts -run '^(TestGeneratedProjectInputManifest|TestConsoleDeploymentIsolation)$'
go test -race -mod=readonly -count=1 -timeout=3m ./internal/databasecontroller ./internal/gatewayworkload ./internal/namespaceallocation ./internal/namespaceallocationapp
cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt /etc/ssl/certs/ca-certificates.crt >> /work/registry-ca.crt
export SSL_CERT_FILE=/work/registry-ca.crt
go run -mod=readonly scripts/service-image-auth.go
registry=image-registry.openshift-image-registry.svc:5000
. /work/application/scripts/publish-service-image.sh
publish_image service ./out hypershell /work/image.json
(cd console; publish_image service ./out hypershell-console /work/console-image.json)
publish_image rpc ./out/grpcapi/processes/provisioner hypershell-provisioner /work/provisioner-image.json
if [ "${STEGO_TEST_BROWSER_WORKLOAD:-0}" = 1 ]; then
 for worker in namespace-allocation database gateway-identity gateway-workload; do
  publish_image worker "./out/deploy/workers/$worker" "hypershell-$worker" "/work/$worker-image.json"
 done
fi
unlink /work/registry-auth.json
digest=$(go run -mod=readonly scripts/service-image-digest.go /work/image.json service)
console_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/console-image.json service)
export STEGO_TEST_SERVICE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell@$digest"
export STEGO_TEST_CONSOLE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-console@$console_digest"
provisioner_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/provisioner-image.json rpc)
export STEGO_TEST_PROVISIONER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-provisioner@$provisioner_digest"
if [ "${STEGO_TEST_BROWSER_WORKLOAD:-0}" = 1 ]; then
 allocation_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/namespace-allocation-image.json worker)
 export STEGO_TEST_ALLOCATION_WORKER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-namespace-allocation@$allocation_digest"
 database_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/database-image.json worker)
 identity_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/gateway-identity-image.json worker)
 gateway_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/gateway-workload-image.json worker)
 export STEGO_TEST_DATABASE_WORKER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-database@$database_digest"
 export STEGO_TEST_IDENTITY_WORKER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-gateway-identity@$identity_digest"
 export STEGO_TEST_GATEWAY_WORKER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-gateway-workload@$gateway_digest"
fi
export STEGO_TEST_OC=/work/oc
export STEGO_BROWSER_ARTIFACT_DIR=/work/browser-artifacts
go test -v -race -mod=readonly -count=1 -timeout=10m -run '^(TestDatabaseClusterAccessRulesThroughGeneratedRuntime|TestDatabaseRetainedReplayThroughGeneratedRuntime|TestDatabaseDeleteReplayThroughGeneratedRuntime|TestRecoveryPagesAvoidTotals|TestDatabaseRecoveryCursorSurvivesEarlierDeletion|TestPlacementWorkflowThroughGeneratedRuntime|TestGatewayNetworkWorkflowThroughGeneratedRuntime|TestGeneratedKubernetesBrowserGatewayWorkflow|TestKubernetesWriteFailurePrivacy)$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum console/out console/.stego/state.yaml console/go.mod console/go.sum
