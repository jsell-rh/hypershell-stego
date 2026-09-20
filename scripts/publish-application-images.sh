#!/bin/sh
# Source only inside the bounded deployment Job after complete generation.
registry=image-registry.openshift-image-registry.svc:5000
# The registry uses its cluster CA. Approved external storage also needs the
# public roots from this pinned SDK image. The compiler checks the exact bundle.
cat /etc/ssl/certs/ca-certificates.crt >> /work/registry-ca.crt
registry_ca_sha256=$(sha256sum /work/registry-ca.crt | cut -d ' ' -f 1)
python3 -I -B /work/image-delivery/tools/application-images.py publish \
  --images /work/image-delivery/images --set-sha256 "$(cat /work/image-delivery/set-sha256)" \
  --source /work/application --compiler /work/image-delivery/compiler/stego-linux-amd64 \
  --compiler-sha256 "$(cat /work/image-delivery/compiler-sha256)" \
  --repository "$registry/$STEGO_TEST_NAMESPACE" --registry-ca /work/registry-ca.crt \
  --registry-ca-sha256 "$registry_ca_sha256" --username serviceaccount \
  --registry-policy /work/image-delivery/registry-policy.json \
  --token-file /var/run/secrets/kubernetes.io/serviceaccount/token --output /work/image-publication
image_reference() {
  python3 -I - /work/image-publication/publication.json "$1" <<'IMAGE_REFERENCE'
import json,sys
from pathlib import Path
print(json.loads(Path(sys.argv[1]).read_text())['images'][sys.argv[2]])
IMAGE_REFERENCE
}
