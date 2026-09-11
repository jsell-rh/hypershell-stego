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
created=false
cleanup() {
  status=$?
  trap - EXIT
  if [[ $created == true ]]; then
    "${oc_cmd[@]}" -n "$namespace" get job service-check -o json > "$results/job-status.json" || true
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
python3 - "$namespace" "$results" <<'PY'
import base64,json,secrets,sys
from pathlib import Path
ns,root=sys.argv[1],Path(sys.argv[2])
job=json.loads(Path('acceptance/kubernetes-service-job.json').read_text().replace('@NAMESPACE@',ns))
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
tar -cf "$results/application.tar" go.mod go.sum service.yaml registry internal contracts acceptance out .stego scripts migrations cmd
sha256sum "$results/application.tar" > "$results/application.sha256"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'mkdir -p /work/application; tar xf - -C /work/application' < "$results/application.tar"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'cat > /work/oc; chmod 755 /work/oc' < "$(command -v oc)"
"${oc_cmd[@]}" -n "$namespace" exec -i "$pod" -c test -- sh -c 'cat > /work/registry-ca.crt' < "$results/registry-ca.crt"
set +e
"${oc_cmd[@]}" --request-timeout=0 -n "$namespace" exec "$pod" -c test -- env "STEGO_TEST_FS_GROUP=$group" sh -c \
  'sh /work/application/scripts/run-service-deployment-pod.sh > /work/deployment.log 2>&1; result=$?; echo "$result" > /work/deployment.exit; exit "$result"'
result=$?
set -e
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- cat /work/deployment.log > "$results/deployment.log"
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- sh -c \
  'cd /work; tar cf - deployment.exit image.json first.sha256 second.sha256 after-tests.sha256 generated.tar 2>/dev/null' > "$results/evidence.tar" || true
"${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- touch /work/collected
if [[ $result == 0 ]]; then
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Complete job/service-check --timeout=60s
else
  "${oc_cmd[@]}" --request-timeout=0 -n "$namespace" wait --for=condition=Failed job/service-check --timeout=60s || true
fi
tail -30 "$results/deployment.log"
exit "$result"
