#!/usr/bin/env bash
set -euo pipefail
[[ $# == 0 ]] || { echo 'This check takes no arguments.' >&2; exit 2; }
: "${STEGO_TEST_CONTEXT:?Set the saved oc context}"
: "${STEGO_TEST_RENDERER:?Set the compiled generated deployment renderer}"
[[ -x $STEGO_TEST_RENDERER ]] || { echo 'The renderer is not executable.' >&2; exit 2; }
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
results=$(mktemp -d "${TMPDIR:-/tmp}/stego-external-egress.XXXXXXXX")
chmod 700 "$results"
namespace="stego-egress-$(openssl rand -hex 4)"
oc_cmd=(oc --context "$STEGO_TEST_CONTEXT" --request-timeout=15s)
created=false
cleanup() {
  status=$?
  trap - EXIT
  if [[ $created == true ]]; then
    "${oc_cmd[@]}" -n "$namespace" get pod probe -o json > "$results/pod-status.json" || true
    "${oc_cmd[@]}" delete namespace "$namespace" --wait=false || true
    "${oc_cmd[@]}" --request-timeout=0 wait --for=delete "namespace/$namespace" --timeout=45s || true
  fi
  echo "External egress results: $results"
  exit "$status"
}
trap cleanup EXIT
printf '%s\n' "$namespace" > "$results/namespace"
sha256sum "$STEGO_TEST_RENDERER" "$project/scripts/check-external-egress.sh" "$project/acceptance/external-egress.json" > "$results/source.sha256"
"${oc_cmd[@]}" -n default get endpointslices -l kubernetes.io/service-name=kubernetes -o json > "$results/endpoints.json"
python3 - "$project" "$results" "$namespace" <<'PY'
import ipaddress,json,sys
from pathlib import Path
project,root,namespace=Path(sys.argv[1]),Path(sys.argv[2]),sys.argv[3]
endpoints=json.loads((root/'endpoints.json').read_text())
choices=[]
for item in endpoints['items']:
    for endpoint in item['endpoints']:
        if endpoint.get('conditions',{}).get('ready') is not True: continue
        for port in item['ports']:
            if port.get('protocol')!='TCP' or port.get('name')!='https': continue
            for value in endpoint['addresses']:
                ip=ipaddress.ip_address(value)
                if ip.version==4: choices.append((str(ip),port['port']))
if not choices: raise SystemExit('This cluster check requires a ready IPv4 Kubernetes API endpoint.')
address,port=sorted(choices)[0]
(root/'address').write_text(address)
(root/'port').write_text(str(port))
(root/'wrong-address').write_text(str(ipaddress.ip_address(int(ipaddress.ip_address(address))+1)))
fixture=(project/'acceptance/external-egress.json').read_text().replace('@NAMESPACE@',namespace)
(root/'fixture.json').write_text(fixture)
deny={'apiVersion':'networking.k8s.io/v1','kind':'NetworkPolicy','metadata':{'name':'deny-probe','namespace':namespace},'spec':{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-database'}},'policyTypes':['Ingress','Egress'],'ingress':[],'egress':[]}}
(root/'deny.json').write_text(json.dumps(deny))
PY
address=$(cat "$results/address")
port=$(cat "$results/port")
wrong_address=$(cat "$results/wrong-address")
"${oc_cmd[@]}" create namespace "$namespace"
created=true
"${oc_cmd[@]}" apply -f "$results/fixture.json"
"${oc_cmd[@]}" -n "$namespace" wait --for=condition=Ready pod/probe --timeout=45s
expect_access() {
  local phase=$1 want=$2 code status consecutive=0 deadline=$((SECONDS+35))
  while (( SECONDS < deadline )); do
    status=0
    code=$("${oc_cmd[@]}" -n "$namespace" exec probe -- curl --silent --show-error --noproxy '*' \
      --connect-timeout 2 --max-time 3 --cacert /identity/ca.crt --header @/work/header \
      --resolve "kubernetes.default.svc:$port:$address" --output /dev/null --write-out '%{http_code}' \
      "https://kubernetes.default.svc:$port/api/v1/namespaces/$namespace/pods/probe" 2>> "$results/probe-errors.log") || status=$?
    if [[ $want == allow && $status == 0 && $code == 200 ]]; then
      printf '%s: verified HTTPS returned 200\n' "$phase" | tee -a "$results/results.log"
      return
    fi
    if [[ $want == deny && $status == 28 && $code == 000 ]]; then
      consecutive=$((consecutive+1))
      if (( consecutive == 3 )); then
        printf '%s: three connection attempts were blocked\n' "$phase" | tee -a "$results/results.log"
        return
      fi
    else
      consecutive=0
    fi
    sleep 1
  done
  echo "The $phase network check failed." >&2
  return 1
}
apply_endpoint() {
  local phase=$1 endpoint=$2
  "$STEGO_TEST_RENDERER" --image "registry.example.test/hypershell@sha256:$(printf 'a%.0s' {1..64})" \
    --namespace "$namespace" --worker database --egress "kubernetes=$endpoint" > "$results/$phase.json"
  python3 - "$results/$phase.json" "$results/$phase-policy.json" <<'PY'
import json,sys
from pathlib import Path
items=json.loads(Path(sys.argv[1]).read_text())['items']
policies=[item for item in items if item['kind']=='NetworkPolicy']
assert len(policies)==1
Path(sys.argv[2]).write_text(json.dumps(policies[0]))
PY
  "${oc_cmd[@]}" apply -f "$results/$phase-policy.json"
}
expect_access baseline allow
"${oc_cmd[@]}" apply -f "$results/deny.json"
expect_access default-deny deny
apply_endpoint allowed "$address:$port"
expect_access declared-endpoint allow
wrong_port=$((port==65535 ? port-1 : port+1))
apply_endpoint wrong-port "$address:$wrong_port"
expect_access wrong-port deny
apply_endpoint wrong-address "$wrong_address:$port"
expect_access wrong-address deny
apply_endpoint recovered "$address:$port"
expect_access recovered allow
printf '0\n' > "$results/result"
