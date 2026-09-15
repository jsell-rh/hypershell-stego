#!/usr/bin/env bash
# Source only from the frozen service runner. The operator namespace remains.
cleanup_browser_ci() {
  if [[ $created == true ]]; then
    "${oc_cmd[@]}" -n "$namespace" get job service-check -o json > "$results/job-status.json" || true
    "${oc_cmd[@]}" -n "$namespace" delete job service-check --ignore-not-found --cascade=foreground --wait=true --timeout=90s || return 1
    "${oc_cmd[@]}" -n "$namespace" delete deployment -l "stego.test/browser-run=$namespace" --cascade=foreground --wait=true --timeout=90s || return 1
    "${oc_cmd[@]}" -n "$namespace" delete pod -l "stego.test/browser-run=$namespace" --wait=true --timeout=90s || return 1
    python3 "$project/scripts/browser-ci-installation.py" cleanup --context "$STEGO_TEST_CONTEXT" --results "$results" || return 1
    "${oc_cmd[@]}" -n "$namespace" delete secret,service,networkpolicy,configmap -l "stego.test/browser-run=$namespace" --wait=true --timeout=60s || return 1
    python3 "$project/scripts/browser-ci-installation.py" verify --context "$STEGO_TEST_CONTEXT" --results "$results" || return 1
  fi
  python3 - "$STEGO_TEST_CONTEXT" "$namespace" "$results" <<'CI_CLEANUP'
import json, subprocess, sys
from pathlib import Path
context, namespace, directory = sys.argv[1:]
command = ['oc', '--context=' + context, '--request-timeout=20s', '-n', namespace]
def read(*args):
    result = subprocess.run(command + ['get', *args, '-o', 'json'], capture_output=True, check=True, timeout=30)
    return json.loads(result.stdout)
for kind in ['pods', 'jobs', 'deployments']:
    if read(kind)['items']:
        raise RuntimeError('Test execution resources remain; keep the Lease')
for kind in ['secrets', 'services', 'networkpolicies', 'configmaps']:
    if read(kind, '-l', 'stego.test/browser-run=' + namespace)['items']:
        raise RuntimeError('Test data remains; keep the Lease')
root = Path(directory)
allocation = root / 'allocation-cleanup.json'
if not allocation.exists():
    allocation = root / 'allocation-preflight.json'
if not allocation.exists() or json.loads(allocation.read_text()).get('allocations_absent') is not True:
    raise RuntimeError('Allocation cleanup is not confirmed; keep the Lease')
(root / 'cleanup.json').write_text(json.dumps({'namespace_retained': namespace, 'test_resources_absent': True, 'allocations_absent': True}) + '\n')
CI_CLEANUP
}
