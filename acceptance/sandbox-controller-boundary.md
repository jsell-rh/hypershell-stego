# Agent Sandbox controller permission review

The reference Hypershell installation selects Agent Sandbox v0.5.4. This review
uses commit `945016a7b97f46cd2edf8633d6b6a22d5355ecc1` from that release.
It does not establish live Sandbox execution or Kata support.

The upstream controller uses a ClusterRoleBinding. Its role can create, change,
and delete Pods, Services, and persistent volume claims across the cluster.
It also manages Sandbox resources and their status, events, and leader leases.
The default conversion webhook setup can change selected CRDs. See the pinned
[role](https://github.com/kubernetes-sigs/agent-sandbox/blob/945016a7b97f46cd2edf8633d6b6a22d5355ecc1/k8s/rbac.generated.yaml)
and [controller installation](https://github.com/kubernetes-sigs/agent-sandbox/blob/945016a7b97f46cd2edf8633d6b6a22d5355ecc1/k8s/controller.yaml).

The pinned entry point has no option to restrict its cache to selected
namespaces. Its optional tracking-label filter applies to the Pod and Service
caches. That filter does not restrict the account's API permissions. A narrower
RoleBinding alone would leave the unchanged process with denied cluster-wide
list and watch requests. See the pinned
[cache options](https://github.com/kubernetes-sigs/agent-sandbox/blob/945016a7b97f46cd2edf8633d6b6a22d5355ecc1/cmd/agent-sandbox-controller/cacheoptions.go),
[manager options](https://github.com/kubernetes-sigs/agent-sandbox/blob/945016a7b97f46cd2edf8633d6b6a22d5355ecc1/cmd/agent-sandbox-controller/manageroptions.go),
and [entry point](https://github.com/kubernetes-sigs/agent-sandbox/blob/945016a7b97f46cd2edf8633d6b6a22d5355ecc1/cmd/agent-sandbox-controller/main.go).

The trust decision remains open:

- Keep the unchanged upstream controller as trusted cluster infrastructure.
  Its cluster-wide workload permissions would remain an explicit part of the
  operator's trust boundary.
- Build an entry point that retains upstream reconciliation code and restricts
  namespace access. STEGO would own the common process, permission, and lifecycle
  support. This needs a maintained controller image. It also needs a separate
  design for namespace changes, watches, leases, and CRD conversion.

No change that depends on this decision has been made. Do not count the generated
Gateway worker's namespace permissions as proof of the external controller's
permission boundary. Keep Gateway role policy and OpenShell Pod policy in
Hypershell. Keep reusable process and permission mechanisms in STEGO.
