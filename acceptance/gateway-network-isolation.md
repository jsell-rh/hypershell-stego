The Gateway workflow does not yet prove network isolation in allocated
namespaces. The source review on 2026-09-15 found no NetworkPolicy in the
namespace allocator or the Gateway resource builder. The controller worker's
own NetworkPolicy protects a different Pod in the control namespace.

A bounded diagnostic called the generated allocator against a TLS API fixture.
`Ensure` returned success after eight writes: one Namespace, one ResourceQuota,
one ClusterRoleBinding, and five RoleBindings. It wrote no NetworkPolicy.
The diagnostic exited with status 1 to report the gap. It did not contact a real
cluster or test network traffic. The [audit record](allocated-network-audit.json)
contains the source identities, hashes, command, and request inventory.
Run `go run -mod=readonly scripts/allocated-network-audit.go` to repeat the
bounded declaration diagnostic. It returns status 1 when the allocator succeeds
without policy writes, status 2 for a setup or allocation error, and status 0
when policy writes exist. Status 0 would still not prove CNI enforcement.

The reference checkout at `14256be29bcfe4fff38bcaf4a41511cb394ea8e1` contains
Gateway and Sandbox ingress policies in
`components/control-plane/manifests/gateway/networkpolicy.yaml`. Its controller
also creates a router ingress policy. The STEGO variant has not delivered these
policies. The reference policies do not define a complete egress policy, so a
copy of those manifests would not prove the required shared-cluster isolation.

Without a policy that selects a Pod, Kubernetes does not restrict that Pod's
traffic through NetworkPolicy. Policies permit traffic additively. Enforcement
also requires a supported network plugin. Saving a policy in the Kubernetes API
does not prove that the plugin has applied it. The test must use fresh
connections because policy changes need not close existing connections. These
limits are described in the [Kubernetes NetworkPolicy documentation](https://kubernetes.io/docs/concepts/services-networking/network-policies/).

The user has been asked whether Gateway server egress must use only
operator-approved destinations. That decision is pending. Sandbox traffic has
its own policy requirements. The separate Sandbox allocation and live Kata test
remain open; the deferred Kata result must not be treated as a pass.

The current source identifies these paths that the complete network contract
must cover:

| Path | Required check |
| --- | --- |
| Approved router to Gateway TCP 8080 | TLS passthrough and authenticated RPC work; unrelated Pods cannot connect |
| Selected controller or test client to Gateway TCP 8080 | Internal checks work only for declared callers |
| Gateway to Kubernetes | API operations work against the selected managed cluster |
| Gateway to PostgreSQL | The selected database server works; another Gateway database remains inaccessible |
| Gateway to the identity provider | Discovery and key refresh work against the selected issuer |
| Gateway name resolution | Only the selected DNS service receives DNS traffic |
| Gateway telemetry | Declared collection paths work without general ingress |
| Gateway and Sandbox traffic | Explicit peers and ports preserve the Sandbox contract and do not expose other namespaces |

This is an inventory from configuration, not a complete observed traffic capture.
The real workflow must identify any additional required path before release.

STEGO must supply common policy construction, input validation, ownership,
reconciliation, and admission protection. Hypershell should declare the domain
peers and ports. Do not add another policy engine to the application.

The application acceptance gate must check more than a rendered manifest:

1. Policies exist before access is granted and before a Gateway Pod is created.
   A policy write failure prevents workload creation.
2. Fresh connections prove allowed traffic and denied traffic in both directions.
   Include an unrelated namespace and a second Gateway namespace.
3. The full policy set is inspected. An added permissive policy cannot silently
   bypass the intended rules.
4. Endpoint removal, policy loss, restart, regeneration, and namespace replacement
   preserve isolation and recover the required paths.
5. DNS, database, identity, telemetry, RPC, and public TLS checks run in the same
   complete workflow. Keep the per-Gateway database, credentials, and data checks.
6. The fixture retains resource, time, and namespace limits and proves cleanup.

The two-Role public permission plan covers only the existing TLS and Route test.
It does not add namespace network isolation. Recompute the plan after generated
policy permissions change; do not widen its current allowlist without review.
Public connectivity and complete shared-cluster isolation are separate results.
