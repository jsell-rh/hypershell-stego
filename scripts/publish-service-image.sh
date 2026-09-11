#!/bin/sh
# Source this helper only inside the bounded image publisher Job.
publish_image() {
 image_entry=$1
 image_target=$2
 image_name=$3
 image_metadata=$4
 image_layer="/work/$image_name-layer"
 mkdir -p "$image_layer/etc/ssl/certs"
 CGO_ENABLED=0 go build -mod=readonly -trimpath -buildvcs=false -o "$image_layer/$image_entry" "$image_target"
 cp /etc/ssl/certs/ca-certificates.crt "$image_layer/etc/ssl/certs/"
 chmod 555 "$image_layer/$image_entry"
 tar --sort=name --mtime=2026-09-11T00:00:00Z --owner=0 --group=0 --numeric-owner -czf "/work/$image_name-layer.tar.gz" -C "$image_layer" .
 image_reference="$registry/$STEGO_TEST_NAMESPACE/$image_name:acceptance"
 /work/oc image append --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt --created-at=2026-09-11T00:00:00Z --image="{\"User\":\"65532:65532\",\"Entrypoint\":[\"/$image_entry\"],\"WorkingDir\":\"/\"}" --meta='{"os":"linux","architecture":"amd64"}' --to="$image_reference" "/work/$image_name-layer.tar.gz"
 /work/oc image info --registry-config=/work/registry-auth.json --certificate-authority=/work/registry-ca.crt -o json "$image_reference" > "$image_metadata"
}
