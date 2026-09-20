#!/usr/bin/env bash
# Source after compiler setup and before the test acquires its cluster Lease.
: "${STEGO_TEST_IMAGE_ARTIFACTS:?Select the downloaded application image artifacts}"
: "${STEGO_TEST_IMAGE_POLICY:?Select an independent trusted application image policy}"
: "${STEGO_TEST_IMAGE_RUN:?Select the image workflow run}"
: "${STEGO_TEST_IMAGE_ATTEMPT:?Select the image workflow attempt}"
test -z "$(git status --porcelain --untracked-files=all)"
python3 -I - "$STEGO_TEST_IMAGE_POLICY" "$(git rev-parse HEAD)" <<'IMAGE_SOURCE'
import json,sys
from pathlib import Path
if json.loads(Path(sys.argv[1]).read_text())['application_revision'] != sys.argv[2]:
    raise SystemExit('The selected images do not use this application commit')
IMAGE_SOURCE
# The signed records cover the complete tracked source. Capture it before the
# test takes the Lease. The Pod checks the snapshot after regeneration.
git archive --format=tar HEAD > "$results/application.tar"
image_revision=$(cat .stego/image-compiler-revision)
image_compiler_sha256=$(cat .stego/image-compiler-sha256)
[[ $image_revision =~ ^[0-9a-f]{40}$ && $image_compiler_sha256 =~ ^[0-9a-f]{64}$ ]]
image_tooling="$results/compiler-setup/tooling/scripts"
image_delivery="$results/image-delivery"
mkdir -m 700 -- "$image_delivery"
timeout --signal=TERM --kill-after=5s 12m python3 -I -B "$image_tooling/install-compiler.py" --release --revision "$image_revision" \
  --gh "$(command -v gh)" --output "$image_delivery/compiler" > "$results/image-compiler-installation.json"
[[ $(sha256sum "$image_delivery/compiler/stego-linux-amd64") == "$image_compiler_sha256  $image_delivery/compiler/stego-linux-amd64" ]]
python3 -I -B "$image_tooling/application-images.py" stage \
  --declaration "$project/.ci/application-images.json" --policy "$STEGO_TEST_IMAGE_POLICY" \
  --artifacts "$STEGO_TEST_IMAGE_ARTIFACTS" --run "$STEGO_TEST_IMAGE_RUN" --attempt "$STEGO_TEST_IMAGE_ATTEMPT" \
  --gh "$(command -v gh)" --output "$image_delivery/images" > "$results/image-selection.json"
mkdir -m 700 -- "$image_delivery/tools"
for file in application-images.py application-build-matrix.py verify-application-records.py verify-compiler-artifact.py check-compiler-artifact.py; do
  cp "$image_tooling/$file" "$image_delivery/tools/"
done
sha256sum "$image_delivery/images/images.json" | cut -d ' ' -f 1 > "$image_delivery/set-sha256"
printf '%s\n' "$image_compiler_sha256" > "$image_delivery/compiler-sha256"
tar -cf "$results/image-delivery.tar" -C "$image_delivery" compiler images tools set-sha256 compiler-sha256
sha256sum "$results/image-delivery.tar" > "$results/image-delivery.sha256"
