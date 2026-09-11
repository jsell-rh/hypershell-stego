#!/usr/bin/env bash
set -euo pipefail

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
export NODE_OPTIONS=--max-old-space-size=1536
pnpm --filter @openshift-online/hypershell-domain-probes build
pnpm --filter @openshift-online/hypershell-gateway-management-ui build
pnpm --filter @openshift-online/hypershell-web-console typecheck
pnpm --filter @openshift-online/hypershell-web-console architecture:check
pnpm --filter @openshift-online/hypershell-web-console lint
for package in hypershell-domain-probes hypershell-gateway-management-ui hypershell-web-console; do
  pnpm --filter "@openshift-online/$package" exec vitest run --maxWorkers=1 --no-file-parallelism
done
pnpm --filter @openshift-online/hypershell-web-console build
scripts/check-console-assets.sh
