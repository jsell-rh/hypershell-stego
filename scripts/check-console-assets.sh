#!/usr/bin/env bash
set -euo pipefail

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
revision=$(cat .stego/compiler-revision)
[[ $revision =~ ^[0-9a-f]{40}$ ]]
output=$(mktemp -d)
trap 'rm -rf -- "$output"' EXIT
if [[ -n ${STEGO_BIN:-} ]]; then
  "$STEGO_BIN" assets --directory components/web-console/build/client --output "$output/build.zip"
else
  GOWORK=off go run "github.com/jsell-rh/stego/cmd/stego@$revision" assets \
    --directory components/web-console/build/client --output "$output/build.zip"
fi
cmp console/ui/build.zip "$output/build.zip"
