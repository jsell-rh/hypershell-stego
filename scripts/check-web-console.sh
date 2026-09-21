#!/usr/bin/env bash
set -euo pipefail
compiler_token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
unset GH_TOKEN GITHUB_TOKEN

project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$project/scripts/check-phases.sh"
check_phases_init web-console
cd "$project"
export NODE_OPTIONS=--max-old-space-size=1536
check_phase_start domain-build
pnpm --filter @openshift-online/hypershell-domain-probes build
check_phase_done
check_phase_start management-build
pnpm --filter @openshift-online/hypershell-gateway-management-ui build
check_phase_done
check_phase_start typecheck
pnpm --filter @openshift-online/hypershell-web-console typecheck
check_phase_done
check_phase_start architecture
pnpm --filter @openshift-online/hypershell-web-console architecture:check
check_phase_done
check_phase_start lint
pnpm --filter @openshift-online/hypershell-web-console lint
check_phase_done
for package in hypershell-domain-probes hypershell-gateway-management-ui hypershell-web-console; do
  check_phase_start "$package-tests"
  pnpm --filter "@openshift-online/$package" exec vitest run --maxWorkers=1 --no-file-parallelism
  check_phase_done
done
check_phase_start console-build
pnpm --filter @openshift-online/hypershell-web-console build
check_phase_done
check_phase_start assets
GH_TOKEN="$compiler_token" scripts/check-console-assets.sh
check_phase_done
check_phase_start regeneration
GH_TOKEN="$compiler_token" scripts/generate.sh --check
check_phase_done
unset compiler_token
