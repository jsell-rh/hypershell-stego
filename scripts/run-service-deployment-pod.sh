#!/bin/sh
# Run only inside the bounded service acceptance Job.
set -eu
trap '[ ! -e /work/registry-auth.json ] || unlink /work/registry-auth.json' EXIT
cd /work/application
# The host authenticated and compared these bytes before it started this Pod.
(cd /work/compiler && sha256sum --check SHA256SUMS)
compiler=/work/compiler/stego-linux-amd64

for pass in first second; do
 "$compiler" apply
 "$compiler" deps
 "$compiler" apply
 "$compiler" drift
 find out -type f -print > /work/generated-files
 printf '%s\n' .stego/state.yaml go.mod go.sum >> /work/generated-files
 sort -o /work/generated-files /work/generated-files
 xargs sha256sum < /work/generated-files > "/work/$pass.sha256"
done
cmp /work/first.sha256 /work/second.sha256
go mod verify
go vet ./acceptance ./out/...
cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt /etc/ssl/certs/ca-certificates.crt >> /work/registry-ca.crt
export SSL_CERT_FILE=/work/registry-ca.crt
go run -mod=readonly scripts/service-image-auth.go
registry=image-registry.openshift-image-registry.svc:5000
. /work/application/scripts/publish-service-image.sh

publish_image service ./out hypershell /work/image.json
publish_image worker ./out/deploy/workers/gateway-identity hypershell-gateway-identity /work/worker-image.json
unlink /work/registry-auth.json
digest=$(go run -mod=readonly scripts/service-image-digest.go /work/image.json service)
worker_digest=$(go run -mod=readonly scripts/service-image-digest.go /work/worker-image.json worker)
export STEGO_TEST_SERVICE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell@$digest"
export STEGO_TEST_WORKER_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell-gateway-identity@$worker_digest"
export STEGO_TEST_OC=/work/oc
go test -v -race -mod=readonly -count=1 -timeout=8m -run '^TestGeneratedKubernetesServiceGatewayWorkflow$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
(cd /work/compiler && sha256sum --check SHA256SUMS)
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum
