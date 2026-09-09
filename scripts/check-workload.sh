#!/usr/bin/env bash
set -euo pipefail
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project"
if [[ $# != 1 || (${1:-} != database && ${1:-} != gateway) || $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'Run scripts/check-workload.sh database or gateway on Linux amd64.' >&2
  exit 2
fi
: "${STEGO_TEST_POSTGRES_DSN:?Set a PostgreSQL connection that can create test databases.}"
workflow=$1
scratch=$(mktemp -d)
cluster="stego-db-$(date +%s)-$$"
cleanup() {
  if [[ -x $scratch/kind ]]; then
    "$scratch/kind" delete cluster --name "$cluster" >/dev/null 2>&1 || true
  fi
  rm -r -- "$scratch"
}
trap cleanup EXIT
fetch() { curl -fsSL --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
fetch https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0/kind-linux-amd64 "$scratch/kind"
fetch https://dl.k8s.io/release/v1.35.8/bin/linux/amd64/kubectl "$scratch/kubectl"
fetch https://github.com/cert-manager/cert-manager/releases/download/v1.21.1/cert-manager.yaml "$scratch/cert-manager.yaml"
(
  cd "$scratch"
  sha256sum --check <<'CHECKSUMS'
aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d  kind
874d5e72dbb819f43cff16bcd1e4f8bac5b7f2361fe1e55049b0a6c676fb0cbf  kubectl
5f6a499b8c1857d57f560f536e0dcc830914b45c420899fe7ad0692c8624e408  cert-manager.yaml
CHECKSUMS
)
chmod 700 "$scratch/kind" "$scratch/kubectl"
export PATH="$scratch:$PATH"
# Pin the three runtime images from the checked release manifest.
sed -i \
  -e 's#quay.io/jetstack/cert-manager-cainjector:v1.21.1#quay.io/jetstack/cert-manager-cainjector@sha256:ccf6b919ec0500745a47a910118f834f9636d0aac1ff221245cd2557ed8c7c98#g' \
  -e 's#quay.io/jetstack/cert-manager-controller:v1.21.1#quay.io/jetstack/cert-manager-controller@sha256:416a2d76870d996460e62bd7f521bf14fa017be9e3e904aab92163a331fcb61a#g' \
  -e 's#quay.io/jetstack/cert-manager-webhook:v1.21.1#quay.io/jetstack/cert-manager-webhook@sha256:d8b3961b51c8c7320633f8208dc46bf88aa13804d0f7cbe48a096b2c523cee42#g' \
  "$scratch/cert-manager.yaml"
export STEGO_TEST_KUBECONFIG="$scratch/kubeconfig"
kind create cluster --name "$cluster" --kubeconfig "$STEGO_TEST_KUBECONFIG" \
  --image kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0 --wait 120s
kubectl --kubeconfig "$STEGO_TEST_KUBECONFIG" apply -f "$scratch/cert-manager.yaml"
kubectl --kubeconfig "$STEGO_TEST_KUBECONFIG" -n cert-manager rollout status deployment/cert-manager-webhook --timeout=120s
kubectl --kubeconfig "$STEGO_TEST_KUBECONFIG" -n cert-manager rollout status deployment/cert-manager --timeout=120s
export STEGO_REQUIRE_POSTGRES=1 STEGO_REQUIRE_KUBERNETES=1 GOWORK=off
if [[ $workflow == gateway ]]; then
  fetch https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.5.4/sandbox.yaml "$scratch/sandbox.yaml"
  (cd "$scratch" && echo '51e3610f235b58abd465280682d366d3d0fed8972489bf6a800d707988d24c3e  sandbox.yaml' | sha256sum --check)
  sed -i 's#registry.k8s.io/agent-sandbox/agent-sandbox-controller:v0.5.4#registry.k8s.io/agent-sandbox/agent-sandbox-controller@sha256:be477ba317d84a13a38d7605e925e7b4aa82de5b313a4274358920310a931b7f#g' "$scratch/sandbox.yaml"
  kubectl --kubeconfig "$STEGO_TEST_KUBECONFIG" apply -f "$scratch/sandbox.yaml"
  kubectl --kubeconfig "$STEGO_TEST_KUBECONFIG" -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=120s
  export STEGO_REQUIRE_KEYCLOAK=1
  go test -race -count=1 ./internal/gatewayworkload
  go test -race -count=1 -v ./acceptance -run '^TestGateway(WorkloadWithDatabaseAndIdentity|DeletionBeforeWorkloadStartup)$'
else
  go test -race -count=1 ./internal/databasecontroller
  go test -race -count=1 -v ./acceptance -run '^TestDatabase(WorkloadAndOfflineDeletion|DeleteReplayThroughGeneratedRuntime)$'
fi
