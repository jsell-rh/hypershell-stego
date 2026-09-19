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

## Selected trust boundary

On 2026-09-19, the user selected the unchanged upstream controller as trusted
cluster infrastructure. Keep its upstream reconciliation code and entry point.
Do not build a namespace-scoped controller image for this integration.

The cluster operator owns the controller installation and its cluster-wide
permissions. These permissions are part of the trusted cluster infrastructure.
They do not belong to a Gateway worker, Sandbox account, or application API
identity. Keep the generated allocator and the application workers within their
existing permission limits. A cache label filter is not an access control.

Use the pinned upstream release and retain the installation source hashes.
Review its permissions when the selected upstream release changes. Do not use
an application worker identity to install the controller or its cluster roles.
Keep Gateway role policy and OpenShell Pod policy in Hypershell. Keep common
allocation, permission checks, and telemetry in STEGO.

This decision resolves the external controller trust question. It does not
change a controller deployment or remove the current constructor guard. The
allocation and native network checks have separate evidence. Live OpenShell
Sandbox execution and Kata isolation remain unverified; the user deferred the
Kata test because no suitable cluster is available. See the
[Sandbox workflow record](sandbox-workflow.md).
