#!/bin/sh
# Run only inside the bounded service acceptance Job.
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
 /work/stego apply
 /work/stego deps
 /work/stego apply
 /work/stego drift
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
publish_image() {
 image_entry=$1
 image_target=$2
 image_name=$3
 image_metadata=$4
 image_layer="/work/$image_entry-layer"
 mkdir -p "$image_layer/etc/ssl/certs"
 CGO_ENABLED=0 go build -mod=readonly -trimpath -buildvcs=false -o "$image_layer/$image_entry" "$image_target"
 cp /etc/ssl/certs/ca-certificates.crt "$image_layer/etc/ssl/certs/"
 chmod 555 "$image_layer/$image_entry"
 tar --sort=name --mtime=2026-09-11T00:00:00Z --owner=0 --group=0 --numeric-owner -czf "/work/$image_entry-layer.tar.gz" -C "$image_layer" .
 image_reference="$registry/$STEGO_TEST_NAMESPACE/$image_name:acceptance"
 /work/oc image append --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt --created-at=2026-09-11T00:00:00Z --image="{\"User\":\"65532:65532\",\"Entrypoint\":[\"/$image_entry\"],\"WorkingDir\":\"/\"}" --meta='{"os":"linux","architecture":"amd64"}' --to="$image_reference" "/work/$image_entry-layer.tar.gz"
 /work/oc image info --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt -o json "$image_reference" > "$image_metadata"
}
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
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum
