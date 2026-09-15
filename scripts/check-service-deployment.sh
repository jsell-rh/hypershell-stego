#!/usr/bin/env bash
# Build and test in a dedicated OpenShift namespace. No local Go build is used.
set -euo pipefail
: "${STEGO_TEST_CONTEXT:?Set the saved oc context for the test cluster}"
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
test "$(uname -s)/$(uname -m)" = Linux/x86_64
revision=$(cat .stego/compiler-revision)
[[ $revision =~ ^[0-9a-f]{40}$ ]]
namespace="stego-service-$(date -u +%Y%m%d)-$(openssl rand -hex 3)"
results=$(mktemp -d "${TMPDIR:-/tmp}/stego-service-results.XXXXXXXX")
chmod 700 "$results"
oc_cmd=(oc --context "$STEGO_TEST_CONTEXT" --request-timeout=30s)
workload=${STEGO_TEST_BROWSER_WORKLOAD:-0}
[[ $workload == 0 || $workload == 1 ]]
if [[ $workload == 1 ]]; then
  [[ ${STEGO_TEST_BROWSER_DEPLOYMENT:-0} == 1 ]]
  : "${STEGO_TEST_GATEWAY_CLUSTER_ISSUER:?Set the existing test ClusterIssuer}"
fi
# Keep the lock helper fixed for this run.
cp scripts/jshell_live_lock.py "$results/"
python3 "$results/jshell_live_lock.py" acquire --context "$STEGO_TEST_CONTEXT" \
  --holder "$namespace" --namespace "$namespace" --job service-check
created=false
cleanup_resources() {
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
      for worker in gateway-workload; do
        "${oc_cmd[@]}" delete "clusterrole/$namespace.hypershell-$worker" "clusterrolebinding/$namespace.hypershell-$worker" --ignore-not-found || true
      done
      "${oc_cmd[@]}" delete clusterrolebinding -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      "${oc_cmd[@]}" delete namespace -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      for role in gateway-state gateway-worker gateway-runtime gateway-reviews sandbox-count proof; do
        "${oc_cmd[@]}" delete "clusterrole/$namespace.hypershell-namespace-allocation.$role" --ignore-not-found || true
      done
      "${oc_cmd[@]}" delete "clusterrole/$namespace.hypershell-namespace-allocation" "clusterrolebinding/$namespace.hypershell-namespace-allocation" --ignore-not-found || true
      for policy in allocation ownership resources; do
        "${oc_cmd[@]}" delete "validatingadmissionpolicy/$namespace.hypershell-namespace-allocation.$policy" "validatingadmissionpolicybinding/$namespace.hypershell-namespace-allocation.$policy" --ignore-not-found || true
      done
      "${oc_cmd[@]}" delete namespace -l "stego.test/browser-run=$namespace" --wait=false || true
      "${oc_cmd[@]}" delete clusterrole,clusterrolebinding -l "stego.test/browser-run=$namespace" --wait=false || true
    fi
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
    python3 "$results/jshell_live_lock.py" release --context "$STEGO_TEST_CONTEXT" \
      --holder "$namespace" || status=1
  else
    status=1
    echo "Cleanup failed. Keep the shared live-test Lease for inspection." >&2
  fi
  for file in "$results/private-job.json" "$results/server.key" "$results/ca.key"; do
    [[ ! -e $file ]] || unlink -- "$file"
  done
  echo "Service deployment results: $results"
  exit "$status"
}
trap cleanup EXIT
umask 077
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
if [[ $workload == 1 ]]; then
  "${oc_cmd[@]}" -n default get endpointslices -l kubernetes.io/service-name=kubernetes -o json > "$results/kubernetes-endpoints.json"
  "${oc_cmd[@]}" -n default get service kubernetes -o json > "$results/kubernetes-service.json"
fi
python3 - "$namespace" "$results" "${STEGO_TEST_BROWSER_DEPLOYMENT:-0}" "$workload" "${STEGO_TEST_GATEWAY_CLUSTER_ISSUER:-}" <<'PY'
import base64,json,secrets,sys
from pathlib import Path
ns,root=sys.argv[1],Path(sys.argv[2])
job=json.loads(Path('acceptance/kubernetes-service-job.json').read_text().replace('@NAMESPACE@',ns))
if sys.argv[3] not in ('0','1'): raise SystemExit('STEGO_TEST_BROWSER_DEPLOYMENT must be 0 or 1')
if sys.argv[3]=='1':
    security={'runAsNonRoot':True,'readOnlyRootFilesystem':True,'allowPrivilegeEscalation':False,'capabilities':{'drop':['ALL']}}
    for item in job['items']:
        if item['kind']=='ResourceQuota': item['spec']['hard'].update({'limits.memory':'8Gi','limits.cpu':'8','pods':'6'})
        if item['kind']=='Role' and item['metadata']['name']=='service-check':
            for rule in item['rules']:
                if 'deployments/scale' in rule['resources']: rule['resourceNames']=['hypershell','hypershell-console','hypershell-provisioner']
        if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
            item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-console'}}}],'ports':[{'port':5432,'protocol':'TCP'},{'port':19093,'protocol':'TCP'}]})
            item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-provisioner'}}}],'ports':[{'port':19093,'protocol':'TCP'}]})
        if item['kind']=='Job':
            spec=item['spec']['template']['spec']
            spec['initContainers'].insert(0,{'name':'node-tools','image':'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393','command':['sh','-c','mkdir -p /work/bin /work/node; cp /usr/local/bin/node /work/bin/node; cp -R /usr/local/lib/node_modules/npm /work/node/npm'],'securityContext':security,'resources':{'requests':{'cpu':'100m','memory':'128Mi'},'limits':{'cpu':'500m','memory':'256Mi'}},'volumeMounts':[{'name':'work','mountPath':'/work'}]})
            spec['initContainers'].append({'name':'chromium','restartPolicy':'Always','image':'docker.io/selenium/standalone-chromium@sha256:81c80050126f610675e40eeac529a821dc5a0d38acf26c6d44f792a6e7ea8ac5','command':['sh','-c','mkdir -p /tmp/config /tmp/cache; exec chromedriver --port=9515 --allowed-ips=127.0.0.1'],'env':[{'name':'XDG_CONFIG_HOME','value':'/tmp/config'},{'name':'XDG_CACHE_HOME','value':'/tmp/cache'}],'securityContext':security,'resources':{'requests':{'cpu':'100m','memory':'256Mi'},'limits':{'cpu':'1','memory':'1536Mi'}},'startupProbe':{'tcpSocket':{'port':9515},'periodSeconds':2,'failureThreshold':30},'volumeMounts':[{'name':'chrometmp','mountPath':'/tmp'}]})
            spec['volumes'].append({'name':'chrometmp','emptyDir':{'sizeLimit':'512Mi'}})
            test=spec['containers'][0]
            test['command'][-1]=test['command'][-1].replace('run-service-deployment-pod.sh','run-browser-deployment-pod.sh')
            test['env'] += [{'name':'STEGO_TEST_KUBERNETES_BROWSER','value':'1'},{'name':'STEGO_REQUIRE_BROWSER','value':'1'},{'name':'PATH','value':'/work/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'}]
if sys.argv[4]=='1':
    import re
    if sys.argv[3]!='1' or not re.fullmatch(r'[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?',sys.argv[5]): raise SystemExit('Invalid Gateway test profile')
    import ipaddress
    endpoints=set()
    for item in json.loads((root/'kubernetes-endpoints.json').read_text())['items']:
        for endpoint in item['endpoints']:
            if endpoint.get('conditions',{}).get('ready') is not True: continue
            for port in item['ports']:
                if port.get('protocol')!='TCP' or port.get('name')!='https': continue
                for address in endpoint['addresses']:
                    ip=ipaddress.ip_address(address)
                    endpoints.add(f'[{ip}]:{port["port"]}' if ip.version==6 else f'{ip}:{port["port"]}')
    service=json.loads((root/'kubernetes-service.json').read_text())
    for address in service['spec'].get('clusterIPs',[service['spec']['clusterIP']]):
        ip=ipaddress.ip_address(address)
        endpoints.add(f'[{ip}]:443' if ip.version==6 else f'{ip}:443')
    if not 1<=len(endpoints)<=16: raise SystemExit('Invalid Kubernetes endpoint set')
    for item in job['items']:
        if item['kind']=='ResourceQuota': item['spec']['hard'].update({'limits.memory':'11Gi','limits.cpu':'12','pods':'10'})
        if item['kind']=='Role' and item['metadata']['name']=='service-check':
            for rule in item['rules']:
                if 'deployments/scale' in rule['resources']: rule['resourceNames'] += ['hypershell-namespace-allocation','hypershell-gateway-identity','hypershell-gateway-workload']
        if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
            for worker in ['namespace-allocation','gateway-identity','gateway-workload']:
                item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-'+worker}}}],'ports':[{'port':19093,'protocol':'TCP'}]})
        if item['kind']=='Job': item['spec']['template']['spec']['containers'][0]['env'].append({'name':'STEGO_TEST_KUBERNETES_EGRESS','value':json.dumps(sorted(endpoints))})
    import hashlib
    marker=hashlib.sha256((ns+'.hypershell-namespace-allocation').encode()).hexdigest()[:32]
    for item in job['items']:
        if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
            item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-gateway-workload'}}}],'ports':[{'port':5432,'protocol':'TCP'}]})
            item['spec']['ingress'].append({'from':[{'namespaceSelector':{'matchLabels':{'stego.dev/allocator':marker,'stego.dev/allocation-profile':'gateway'}}}],'ports':[{'port':5432,'protocol':'TCP'}]})
    role=json.loads(Path('acceptance/browser-workload-rbac.json').read_text().replace('@NAMESPACE@',ns).replace('@ALLOCATOR_MARKER@',marker))
    job['items'] += role['items']
    for item in job['items']:
        if item['kind']=='Job': item['spec']['template']['spec']['containers'][0]['env'] += [{'name':'STEGO_TEST_BROWSER_WORKLOAD','value':'1'},{'name':'STEGO_TEST_GATEWAY_CLUSTER_ISSUER','value':sys.argv[5]}]
password=secrets.token_hex(24)
encode=lambda value:base64.b64encode(value.encode()).decode()
for item in job['items']:
    if item['kind']=='Secret' and item['metadata']['name']=='cli-test-postgres':
        item['data']={key:encode(value) for key,value in {
          'password':password,
          'dsn':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt',
          'url':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt'
        }.items()}
    if item['kind']=='Secret' and item['metadata']['name']=='database-tls':
        item['data']={name:encode((root/name).read_text()) for name in ['server.key','server.crt']}
    if item['kind']=='ConfigMap' and item['metadata']['name']=='database-ca':
        item['data']={'server.crt':(root/'ca.crt').read_text()}
(root/'private-job.json').write_text(json.dumps(job))
for item in job['items']:
    if item['kind']=='Secret':item.pop('data',None)
(root/'job.json').write_text(json.dumps(job,indent=2)+'\n')
PY
# Create the namespace first so cleanup also runs after a partial apply.
"${oc_cmd[@]}" create namespace "$namespace" --save-config
created=true
"${oc_cmd[@]}" apply -f "$results/private-job.json"
for file in "$results/private-job.json" "$results/server.key"; do
    [[ ! -e $file ]] || unlink -- "$file"
  done
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for='jsonpath={.status.active}=1' job/service-check --timeout=180s
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Ready pod -l job-name=service-check --timeout=180s
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
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- cat /work/deployment.log > "$results/deployment.log"
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- sh -c \
  'cd /work; set --; for file in deployment.exit image.json console-image.json worker-image.json provisioner-image.json namespace-allocation-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts; do if [ -e "$file" ]; then set -- "$@" "$file"; fi; done; tar cf - "$@"' > "$results/evidence.tar" || true
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- touch /work/collected
if [[ $result == 0 ]]; then
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Complete job/service-check --timeout=60s
else
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Failed job/service-check --timeout=60s || true
fi
tail -30 "$results/deployment.log"
exit "$result"
