#!/usr/bin/env bash
# Build application images and test in a dedicated OpenShift namespace.
# The operator compiles only the small deployment renderer on the host.
set -euo pipefail
: "${STEGO_TEST_CONTEXT:?Set the saved oc context for the test cluster}"
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
test "$(uname -s)/$(uname -m)" = Linux/x86_64
revision=$(cat .stego/compiler-revision)
[[ $revision =~ ^[0-9a-f]{40}$ ]]
preinstalled=${STEGO_TEST_PREINSTALLED:-0}
[[ $preinstalled == 0 || $preinstalled == 1 ]]
namespace="stego-service-$(date -u +%Y%m%d)-$(openssl rand -hex 3)"
if [[ $preinstalled == 1 ]]; then namespace=stego-service-ci; fi
if [[ -n ${STEGO_TEST_RESULTS:-} ]]; then
  results=$STEGO_TEST_RESULTS
  mkdir -m 700 -- "$results"
else
  results=$(mktemp -d "${TMPDIR:-/tmp}/stego-service-results.XXXXXXXX")
fi
chmod 700 "$results"
oc_cmd=(oc --context "$STEGO_TEST_CONTEXT" --request-timeout=30s)
workload=${STEGO_TEST_BROWSER_WORKLOAD:-0}
[[ $workload == 0 || $workload == 1 ]]
if [[ $preinstalled == 1 ]]; then [[ $workload == 1 ]]; fi
if [[ $workload == 1 ]]; then
  [[ ${STEGO_TEST_BROWSER_DEPLOYMENT:-0} == 1 ]]
  : "${STEGO_TEST_GATEWAY_CLUSTER_ISSUER:?Set the existing test ClusterIssuer}"
  : "${STEGO_TEST_GATEWAY_INTERNAL_CA_FILE:?Set the operator-supplied internal Gateway CA file}"
  test -s acceptance/browser-inspection-source.json
fi
# Check lifetime before acquiring a Lease or changing the test installation.
if [[ $preinstalled == 1 ]]; then
  python3 scripts/ci_credentials.py --context "$STEGO_TEST_CONTEXT" --gate browser
fi
# Keep the lock helper fixed for this run.
cp scripts/jshell_live_lock.py "$results/"
held_lease=${STEGO_TEST_HELD_LEASE_HOLDER:-}
held_lease_uid=${STEGO_TEST_HELD_LEASE_UID:-}
if [[ -n $held_lease || -n $held_lease_uid ]]; then
  [[ -n $held_lease && -n $held_lease_uid && $preinstalled == 1 ]]
  python3 "$results/jshell_live_lock.py" verify --context "$STEGO_TEST_CONTEXT" \
    --holder "$held_lease" --uid "$held_lease_uid" --namespace "$namespace" --job service-check
else
  python3 "$results/jshell_live_lock.py" acquire --context "$STEGO_TEST_CONTEXT" \
    --holder "$namespace" --namespace "$namespace" --job service-check
fi
created=false
cleanup_resources() {
  if [[ $preinstalled == 1 ]]; then
    source "$project/scripts/cleanup-browser-ci.sh"
    cleanup_browser_ci
    return $?
  fi
  if [[ $created == true ]]; then
    "${oc_cmd[@]}" -n "$namespace" get job service-check -o json > "$results/job-status.json" || true
    # Stop test processes before namespace or permission cleanup.
    "${oc_cmd[@]}" -n "$namespace" delete job service-check --ignore-not-found \
      --cascade=foreground --wait=true --timeout=90s || return 1

    if [[ $workload == 1 ]]; then
      "${oc_cmd[@]}" -n "$namespace" delete deployment --all --cascade=foreground --wait=true --timeout=90s || return 1
      allocation_marker=$(python3 -c 'import hashlib,sys; print(hashlib.sha256((sys.argv[1]+".hypershell-namespace-allocation").encode()).hexdigest()[:32])' "$namespace")
      "${oc_cmd[@]}" get namespace -l "stego.test/browser-run=$namespace" -o name > "$results/owned-namespaces.txt" || true
      "${oc_cmd[@]}" get namespace -l "stego.dev/allocator=$allocation_marker" -o name >> "$results/owned-namespaces.txt" || true
      sort -u -o "$results/owned-namespaces.txt" "$results/owned-namespaces.txt"
      while read -r target; do
        gateway_id=$("${oc_cmd[@]}" get "$target" -o 'jsonpath={.metadata.labels.hypershell\.redhat\.io/gateway-id}' 2>/dev/null) || continue
        if [[ $gateway_id =~ ^[0-9A-Za-z]{27}$ ]]; then
          "${oc_cmd[@]}" delete clusterrole,clusterrolebinding -l "hypershell.redhat.io/gateway-id=$gateway_id,app.kubernetes.io/managed-by=hypershell-gateway-controller" --wait=false || true
        fi
      done < "$results/owned-namespaces.txt"
      "${oc_cmd[@]}" delete clusterrolebinding -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      "${oc_cmd[@]}" delete namespace -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      "${oc_cmd[@]}" delete namespace -l "stego.test/browser-run=$namespace" --wait=false || true
      "${oc_cmd[@]}" delete clusterrole,clusterrolebinding -l "stego.test/browser-run=$namespace" --wait=false || true
    fi
    python3 scripts/prepare-browser-cluster.py --cleanup --context "$STEGO_TEST_CONTEXT" --namespace "$namespace" --results "$results" || return 1
    "${oc_cmd[@]}" delete namespace "$namespace" --wait=false || true
    "${oc_cmd[@]}" --request-timeout=0 wait --for=delete namespace "$namespace" --timeout=60s || true
  fi
  # Check absence before releasing the shared Lease. Read errors retain the Lease.
  python3 - "$STEGO_TEST_CONTEXT" "$namespace" "$results" <<'CHECK_CLEANUP'
import hashlib, json, subprocess, sys, time
from pathlib import Path
context, namespace, directory = sys.argv[1:]
root = Path(directory)
marker = hashlib.sha256((namespace + '.hypershell-namespace-allocation').encode()).hexdigest()[:32]
command = ['oc', '--context', context, '--request-timeout=20s']
def get(*words):
    result = subprocess.run(command + ['get', *words, '-o', 'json'], check=True, capture_output=True, text=True, timeout=30)
    return json.loads(result.stdout) if result.stdout.strip() else None
for attempt in range(12):
    remaining = []
    for kind in ['namespaces', 'clusterroles', 'clusterrolebindings', 'validatingadmissionpolicies', 'validatingadmissionpolicybindings']:
        for obj in get(kind)['items']:
            meta = obj['metadata']
            labels = meta.get('labels', {})
            if meta['name'] == namespace or meta['name'].startswith(namespace + '.') or labels.get('stego.test/browser-run') == namespace or labels.get('stego.dev/allocator') == marker:
                remaining.append(kind + '/' + meta['name'])
    if not remaining:
        (root / 'cleanup.json').write_text(json.dumps({'namespace_absent': namespace, 'owned_resources_absent': True}) + '\n')
        break
    if attempt == 11:
        raise RuntimeError('Resources remain; keep the live-test Lease: ' + ', '.join(remaining))
    time.sleep(5)
CHECK_CLEANUP
}
cleanup() {
  status=$?
  trap - EXIT
  if cleanup_resources; then
    if [[ -n $held_lease ]]; then
      # The outer installation runner must remove its server before release.
      python3 "$results/jshell_live_lock.py" verify --context "$STEGO_TEST_CONTEXT" \
        --holder "$held_lease" --uid "$held_lease_uid" --namespace "$namespace" --job service-check || status=1
    else
      python3 "$results/jshell_live_lock.py" release --context "$STEGO_TEST_CONTEXT" \
        --holder "$namespace" || status=1
    fi
  else
    status=1
    echo "Cleanup failed. Keep the shared live-test Lease for inspection." >&2
  fi
  for file in "$results/private-job.json" "$results/server.key" "$results/ca.key" "$results/ci-token"; do
    [[ ! -e $file ]] || unlink -- "$file"
  done
  echo "Service deployment results: $results"
  exit "$status"
}
trap cleanup EXIT
umask 077
if [[ $preinstalled == 1 ]]; then
  test -z "$("${oc_cmd[@]}" -n "$namespace" get job service-check --ignore-not-found -o name)"
  python3 scripts/browser-ci-installation.py prepare --context "$STEGO_TEST_CONTEXT" --results "$results"
  test "$(cat "$results/issuer")" = "$STEGO_TEST_GATEWAY_CLUSTER_ISSUER"
  python3 scripts/verify-browser-ci.py --context "$STEGO_TEST_CONTEXT" --results "$results"
fi
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$results/ca.key" -out "$results/ca.crt" -days 2 \
  -subj /CN=fixture-ca -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign' >/dev/null 2>&1
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$results/server.key" -out "$results/server.csr" \
  -subj /CN=fixture >/dev/null 2>&1
cat > "$results/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,DNS:fixture.$namespace.svc,IP:127.0.0.1
EOF
openssl x509 -req -in "$results/server.csr" -CA "$results/ca.crt" \
  -CAkey "$results/ca.key" -set_serial 1 -days 2 -extfile "$results/server.ext" \
  -out "$results/server.crt" >/dev/null 2>&1
unlink "$results/ca.key"
openssl verify -CAfile "$results/ca.crt" -purpose sslserver \
  -verify_hostname "fixture.$namespace.svc" "$results/server.crt" >/dev/null
openssl verify -CAfile "$results/ca.crt" -purpose sslserver \
  -verify_ip 127.0.0.1 "$results/server.crt" >/dev/null
if [[ $workload == 1 && $preinstalled == 0 ]]; then
  "${oc_cmd[@]}" -n default get endpointslices -l kubernetes.io/service-name=kubernetes -o json > "$results/kubernetes-endpoints.json"
  "${oc_cmd[@]}" -n default get service kubernetes -o json > "$results/kubernetes-service.json"
fi
python3 scripts/render-service-fixture.py "$namespace" "$results" "${STEGO_TEST_BROWSER_DEPLOYMENT:-0}" "$workload" "${STEGO_TEST_GATEWAY_CLUSTER_ISSUER:-}"
# Persistent CI keeps the operator installation and replaces only test data.
if [[ $preinstalled == 1 ]]; then
  python3 - "$results" <<'CI_OBJECTS'
import json, sys
from pathlib import Path
for name in ['private-job.json', 'job.json']:
    path = Path(sys.argv[1]) / name
    document = json.loads(path.read_text())
    document['items'] = [item for item in document['items'] if item['kind'] in {'Secret', 'ConfigMap', 'Service', 'Job'}]
    path.write_text(json.dumps(document, indent=2) + '\n')
CI_OBJECTS
else
  "${oc_cmd[@]}" create namespace "$namespace" --save-config
fi
created=true
"${oc_cmd[@]}" apply -f "$results/private-job.json"
for file in "$results/private-job.json" "$results/server.key"; do
    [[ ! -e $file ]] || unlink -- "$file"
  done
startup_failed() {
  python3 "$project/scripts/collect-service-startup.py" --context "$STEGO_TEST_CONTEXT" \
    --namespace "$namespace" --output "$results/startup.log" || true
  exit 1
}
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for='jsonpath={.status.active}=1' job/service-check --timeout=180s || startup_failed
# Allow node startup after autoscaling. The Job keeps its total time limit.
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Ready pod -l job-name=service-check --timeout=300s || startup_failed
pod=$("${oc_cmd[@]}" -n "$namespace" get pod -l job-name=service-check -o jsonpath='{.items[0].metadata.name}')
# The test can restart only its own database sidecar and the identity fixture.
# Bind exec permission to this exact Pod name before the frozen test starts.
"${oc_cmd[@]}" -n "$namespace" get role service-check -o json > "$results/fixture-role.json"
python3 - "$pod" "$results" <<'EXEC_PERMISSION'
import json, re, sys
from pathlib import Path
pod, directory = sys.argv[1], Path(sys.argv[2])
if not re.fullmatch(r'service-check-[a-z0-9]+', pod):
    raise SystemExit('The fixture Pod name is invalid')
role = json.loads((directory / 'fixture-role.json').read_text())
matches = [i for i, rule in enumerate(role['rules']) if rule.get('apiGroups') == [''] and rule.get('resources') == ['pods/exec']]
if len(matches) != 1:
    raise SystemExit('The fixture exec rule is missing or repeated')
index = matches[0]
if role['rules'][index].get('resourceNames') != ['identity-fixture'] or set(role['rules'][index].get('verbs', [])) != {'get', 'create'}:
    raise SystemExit('The fixture exec rule differs')
patch = [
    {'op': 'test', 'path': '/metadata/uid', 'value': role['metadata']['uid']},
    {'op': 'test', 'path': '/metadata/resourceVersion', 'value': role['metadata']['resourceVersion']},
    {'op': 'add', 'path': f'/rules/{index}/resourceNames/-', 'value': pod},
]
(directory / 'fixture-exec-patch.json').write_text(json.dumps(patch) + '\n')
EXEC_PERMISSION
"${oc_cmd[@]}" -n "$namespace" patch role service-check --type=json --patch-file="$results/fixture-exec-patch.json"
group=$("${oc_cmd[@]}" get namespace "$namespace" -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.supplemental-groups}')
group=${group%%/*}
[[ $group =~ ^[1-9][0-9]*$ ]]
if [[ ${STEGO_TEST_BROWSER_DEPLOYMENT:-0} == 1 ]]; then
  cluster_args=(--context "$STEGO_TEST_CONTEXT" --namespace "$namespace" --fs-group "$group" --results "$results")
  if [[ $workload == 1 ]]; then cluster_args+=(--workload); fi
  if [[ $preinstalled == 1 ]]; then
    python3 scripts/browser-ci-installation.py verify --context "$STEGO_TEST_CONTEXT" --results "$results"
  else
    python3 scripts/prepare-browser-cluster.py "${cluster_args[@]}"
  fi
  tar -cf "$results/cluster-manifests.tar" -C "$results" cluster-manifests
  "${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- tar xf - -C /work < "$results/cluster-manifests.tar"
fi
"${oc_cmd[@]}" -n "$namespace" get configmap openshift-service-ca.crt -o jsonpath='{.data.service-ca\.crt}' > "$results/registry-ca.crt"
test -s "$results/registry-ca.crt"
tar -cf "$results/application.tar" go.mod go.sum service.yaml registry internal contracts acceptance out .stego scripts migrations cmd console
sha256sum "$results/application.tar" > "$results/application.sha256"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'mkdir -p /work/application; tar xf - -C /work/application' < "$results/application.tar"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'cat > /work/oc; chmod 755 /work/oc' < "$(command -v oc)"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'cat > /work/registry-ca.crt' < "$results/registry-ca.crt"
printf '%s\n' "$group" | "${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'cat > /work/fs-group'
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- touch /work/start
source "$project/scripts/wait-service-result.sh"
wait_service_result
source "$project/scripts/collect-service-evidence.sh"
evidence_status=0
collect_service_evidence || evidence_status=$?
timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- touch /work/collected
if [[ $result == 0 ]]; then
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Complete job/service-check --timeout=60s
else
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Failed job/service-check --timeout=60s || true
fi
tail -30 "$results/deployment.log"
if [[ $result == 0 && $evidence_status != 0 ]]; then exit 1; fi
exit "$result"
