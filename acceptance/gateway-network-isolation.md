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

STEGO commit `d709240557c91f7b415262b35de107c4d70f987e` adds the common
profile option `network_isolation: true`. It creates and verifies a fixed
`stego-allocation` deny-all policy before access bindings. The allocator cannot
patch or delete it. Generated admission rules restrict creation and protect the
reserved name. A read-only allocation check also requires the policy.

Focused generated runtime tests passed for restart, policy loss, invalid policy
contents, API errors, and cancellation. Manifest checks passed. Full compiler CI
passed in [run 34981210745](https://github.com/jsell-rh/stego/actions/runs/34981210745).
Live admission and CNI tests have not run. The
[common mechanism](https://github.com/jsell-rh/stego/blob/d709240557c91f7b415262b35de107c4d70f987e/specs/namespace-allocation.md#fixed-network-deny-policy)
does not yet supply allowed destinations or protect the complete policy set.
Hypershell has not enabled it. Enabling deny-all without the required allowed
paths would stop Gateway service traffic. The full application gate above
remains required.

The earlier [core CI run](gateway-core-ci-20260915.json) passed core and ordinary
browser acceptance. It used compiler `4f692d0`, before the production RPC
lifecycle correction. Its CNPG job failed credential validation before test
creation. It supplies no network isolation result.

The compiler pin and generated output now include `d709240`. Regeneration passed
with no drift in the application and console. Production allocation settings
are unchanged. Its diagnostic still returns status 1 for the known network gap.

A [frozen declaration check](allocated-network-opt-in-fixture.json) enabled
`network_isolation` only in a temporary copy. Regeneration passed. The generated
allocator then wrote nine resources against the local TLS API fixture. The deny
NetworkPolicy was third, after the Namespace and quota and before all bindings.
The diagnostic returned status 0. This proves the declared option changes the
Hypershell allocator through STEGO. It does not prove live admission, network
enforcement, permitted service traffic, or complete application behavior.
