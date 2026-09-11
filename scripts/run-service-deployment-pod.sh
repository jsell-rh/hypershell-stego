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
mkdir -p /work/layer/etc/ssl/certs
CGO_ENABLED=0 go build -mod=readonly -trimpath -buildvcs=false -o /work/layer/service ./out
cp /etc/ssl/certs/ca-certificates.crt /work/layer/etc/ssl/certs/
chmod 555 /work/layer/service
tar --sort=name --mtime=2026-09-11T00:00:00Z --owner=0 --group=0 --numeric-owner -czf /work/service-layer.tar.gz -C /work/layer .
cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt /etc/ssl/certs/ca-certificates.crt >> /work/registry-ca.crt
export SSL_CERT_FILE=/work/registry-ca.crt
go run -mod=readonly scripts/service-image-auth.go
registry=image-registry.openshift-image-registry.svc:5000
reference="$registry/$STEGO_TEST_NAMESPACE/hypershell:acceptance"
/work/oc image append --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt --created-at=2026-09-11T00:00:00Z --image='{"User":"65532:65532","Entrypoint":["/service"],"WorkingDir":"/"}' --meta='{"os":"linux","architecture":"amd64"}' --to="$reference" /work/service-layer.tar.gz
/work/oc image info --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt -o json "$reference" > /work/image.json
unlink /work/registry-auth.json
digest=$(go run -mod=readonly scripts/service-image-digest.go /work/image.json)
export STEGO_TEST_SERVICE_IMAGE="$registry/$STEGO_TEST_NAMESPACE/hypershell@$digest"
export STEGO_TEST_OC=/work/oc
go test -v -race -mod=readonly -count=1 -timeout=8m -run '^TestGeneratedKubernetesServiceGatewayWorkflow$' ./acceptance
xargs sha256sum < /work/generated-files > /work/after-tests.sha256
cmp /work/first.sha256 /work/after-tests.sha256
tar cf /work/generated.tar out .stego/state.yaml .stego/compiler-revision go.mod go.sum
