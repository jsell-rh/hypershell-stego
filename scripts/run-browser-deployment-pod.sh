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
cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt /etc/ssl/certs/ca-certificates.crt >> /work/registry-ca.crt
export SSL_CERT_FILE=/work/registry-ca.crt
go run -mod=readonly scripts/service-image-auth.go
registry=image-registry.openshift-image-registry.svc:5000
. /work/application/scripts/publish-service-image.sh
publish_image service ./out hypershell /work/image.json
(cd console; publish_image service ./out hypershell-console /work/console-image.json)
unlink /work/registry-auth.json
digest=$(go run -mod=readonly scripts/service-image-digest.go /work/image.json service)
console_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/console-image.json service)
export STEGO_TEST_SERVICE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell@$digest"
export STEGO_TEST_CONSOLE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-console@$console_digest"
export STEGO_TEST_OC=/work/oc
export STEGO_BROWSER_ARTIFACT_DIR=/work/browser-artifacts
go test -v -race -mod=readonly -count=1 -timeout=10m -run '^(TestGeneratedKubernetesBrowserGatewayWorkflow|TestKubernetesWriteFailurePrivacy)$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum console/out console/.stego/state.yaml console/go.mod console/go.sum
