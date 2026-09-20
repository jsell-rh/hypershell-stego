#!/bin/sh
# Run only inside the bounded service deployment Job.
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
go mod verify
go vet ./acceptance ./out/...
. /work/application/scripts/publish-application-images.sh
STEGO_TEST_SERVICE_IMAGE="$(image_reference hypershell)"
STEGO_TEST_WORKER_IMAGE="$(image_reference hypershell-gateway-identity)"
export STEGO_TEST_SERVICE_IMAGE STEGO_TEST_WORKER_IMAGE
export STEGO_TEST_OC=/work/oc
go test -v -race -mod=readonly -count=1 -timeout=8m -run '^TestGeneratedKubernetesServiceGatewayWorkflow$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
(cd /work/compiler && sha256sum --check SHA256SUMS)
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum console/out console/.stego/state.yaml console/go.mod console/go.sum gateway-console/out gateway-console/.stego/state.yaml gateway-console/.stego/compiler-revision gateway-console/go.mod gateway-console/go.sum
