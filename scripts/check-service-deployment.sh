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
created=false
cleanup() {
  status=$?
  trap - EXIT
  if [[ $created == true ]]; then
    "${oc_cmd[@]}" -n "$namespace" get job service-check -o json > "$results/job-status.json" || true
    if [[ $workload == 1 ]]; then
      "${oc_cmd[@]}" -n "$namespace" scale deployment --all --replicas=0 >/dev/null 2>&1 || true
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
      for worker in database gateway-workload; do
        "${oc_cmd[@]}" delete "clusterrole/$namespace.hypershell-$worker" "clusterrolebinding/$namespace.hypershell-$worker" --ignore-not-found || true
      done
      "${oc_cmd[@]}" delete clusterrolebinding -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      "${oc_cmd[@]}" delete namespace -l "stego.dev/allocator=$allocation_marker" --wait=false || true
      for role in database-worker database-keys gateway-worker gateway-runtime gateway-reviews proof; do
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
  for file in "$results/private-job.json" "$results/server.key"; do
    [[ ! -e $file ]] || unlink -- "$file"
  done
  echo "Service deployment results: $results"
  exit "$status"
}
trap cleanup EXIT
umask 077
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$results/server.key" -out "$results/server.crt" -days 2 \
  -subj /CN=fixture -addext "subjectAltName=DNS:localhost,DNS:fixture.$namespace.svc,IP:127.0.0.1" >/dev/null 2>&1
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
            spec['volumes'].append({'name':'chrometmp','emptyDir':{'sizeLimit':'256Mi'}})
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
                if 'deployments/scale' in rule['resources']: rule['resourceNames'] += ['hypershell-namespace-allocation','hypershell-database','hypershell-gateway-identity','hypershell-gateway-workload']
        if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
            for worker in ['namespace-allocation','database','gateway-identity','gateway-workload']:
                item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-'+worker}}}],'ports':[{'port':19093,'protocol':'TCP'}]})
        if item['kind']=='Job': item['spec']['template']['spec']['containers'][0]['env'].append({'name':'STEGO_TEST_KUBERNETES_EGRESS','value':json.dumps(sorted(endpoints))})
    import hashlib
    marker=hashlib.sha256((ns+'.hypershell-namespace-allocation').encode()).hexdigest()[:32]
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
          'dsn':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=disable',
          'url':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt'
        }.items()}
    if item['kind']=='Secret' and item['metadata']['name']=='database-tls':
        item['data']={name:encode((root/name).read_text()) for name in ['server.key','server.crt']}
    if item['kind']=='ConfigMap' and item['metadata']['name']=='database-ca':
        item['data']={'server.crt':(root/'server.crt').read_text()}
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
  'cd /work; set --; for file in deployment.exit image.json console-image.json worker-image.json provisioner-image.json namespace-allocation-image.json database-image.json gateway-identity-image.json gateway-workload-image.json first.sha256 second.sha256 after-tests.sha256 generated.tar browser-artifacts; do if [ -e "$file" ]; then set -- "$@" "$file"; fi; done; tar cf - "$@"' > "$results/evidence.tar" || true
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- touch /work/collected
if [[ $result == 0 ]]; then
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Complete job/service-check --timeout=60s
else
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Failed job/service-check --timeout=60s || true
fi
tail -30 "$results/deployment.log"
exit "$result"
