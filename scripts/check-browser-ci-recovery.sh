#!/usr/bin/env bash
# Check cleanup with owned allocations after the failed test Pod is gone.
set -euo pipefail
: "${STEGO_TEST_CONTEXT:?Set the restricted CI context}"
: "${STEGO_TEST_RESULTS:?Set a new results directory}"
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
test -s acceptance/browser-inspection-source.json
namespace=stego-service-ci
results=$STEGO_TEST_RESULTS
mkdir -m 700 -- "$results"
umask 077
oc_cmd=(oc --context "$STEGO_TEST_CONTEXT" --request-timeout=30s)
python3 scripts/jshell_live_lock.py acquire --context "$STEGO_TEST_CONTEXT" \
  --holder browser-ci-recovery --namespace "$namespace" --job service-check
created=false
cleanup() {
  status=$?
  trap - EXIT
  source "$project/scripts/cleanup-browser-ci.sh"
  if cleanup_browser_ci; then
    python3 scripts/jshell_live_lock.py release --context "$STEGO_TEST_CONTEXT" --holder browser-ci-recovery || status=1
  else
    status=1
    echo 'Recovery cleanup failed. Keep the shared Lease for inspection.' >&2
  fi
  [[ ! -e $results/ci-token ]] || unlink -- "$results/ci-token"
  if [[ $status == 0 ]]; then
    python3 - "$results" <<'VERIFY'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
cleanup = json.loads((root / 'allocation-cleanup.json').read_text())
if cleanup != {'allocations_before': 2, 'allocations_absent': True}:
    raise RuntimeError('The recovery check did not remove both allocations')
print('Cleanup passed after the failed Job and its Pod were removed.')
VERIFY
    status=$?
  fi
  echo "Browser CI recovery results: $results"
  exit "$status"
}
trap cleanup EXIT
test -z "$("${oc_cmd[@]}" -n "$namespace" get jobs,pods -o name)"
python3 scripts/browser-ci-installation.py prepare --context "$STEGO_TEST_CONTEXT" --results "$results"
python3 scripts/verify-browser-ci.py --context "$STEGO_TEST_CONTEXT" --results "$results"
timeout 60s env GOMAXPROCS=1 GOMEMLIMIT=256MiB GOWORK=off go build -p=1 -mod=readonly -trimpath \
  -o "$results/allocation-recovery" scripts/browser-allocation-recovery.go
ca=$results/ci-ca.pem
[[ -s $ca ]] || ca=''
STEGO_ALLOCATION_NETWORK_ENDPOINTS=$(python3 scripts/kubernetes_endpoint_bindings.py "$results")
export STEGO_ALLOCATION_NETWORK_ENDPOINTS
created=true
"$results/allocation-recovery" --server "$(cat "$results/ci-server")" --ca-file "$ca" \
  --token-file "$results/ci-token" --result "$results/recovery-allocations.json"
python3 - "$namespace" "$results" <<'FIXTURE'
import json, sys
from pathlib import Path
namespace, directory = sys.argv[1:]
metadata = lambda name: {'name': name, 'namespace': namespace, 'labels': {'stego.test/browser-run': namespace}}
job = {'apiVersion': 'batch/v1', 'kind': 'Job', 'metadata': metadata('service-check'),
       'spec': {'backoffLimit': 0, 'activeDeadlineSeconds': 30, 'ttlSecondsAfterFinished': 3600,
                'template': {'spec': {'restartPolicy': 'Never', 'serviceAccountName': 'service-check', 'automountServiceAccountToken': True,
                'securityContext': {'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}},
                'containers': [{'name': 'failure', 'image': 'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393',
                'command': ['sh', '-c', 'exit 23'], 'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                'resources': {'requests': {'cpu': '100m', 'memory': '64Mi'}, 'limits': {'cpu': '100m', 'memory': '64Mi', 'ephemeral-storage': '16Mi'}}}]}}}}
secret = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': metadata('cli-test-postgres'), 'type': 'Opaque', 'stringData': {'fixture': 'recovery-check'}}
config = {'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': metadata('database-ca'), 'data': {'fixture': 'recovery-check'}}
Path(directory, 'recovery-fixture.json').write_text(json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': [secret, config, job]}, indent=2) + '\n')
FIXTURE
"${oc_cmd[@]}" create -f "$results/recovery-fixture.json"
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Failed job/service-check --timeout=90s
"${oc_cmd[@]}" -n "$namespace" get job service-check -o json > "$results/recovery-job.json"
"${oc_cmd[@]}" -n "$namespace" get pods -l job-name=service-check -o json > "$results/recovery-pods.json"
python3 - "$results" <<'FAILED'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
pods = json.loads((root / 'recovery-pods.json').read_text())['items']
if len(pods) != 1 or pods[0]['status']['containerStatuses'][0]['state']['terminated']['exitCode'] != 23:
    raise RuntimeError('The fixture did not fail with the expected exit code')
FAILED
"${oc_cmd[@]}" -n "$namespace" delete job service-check --cascade=foreground --wait=true --timeout=90s
test -z "$("${oc_cmd[@]}" -n "$namespace" get pods -l job-name=service-check -o name)"
