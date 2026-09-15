The working candidate now enables Gateway and state namespace policies. The
Gateway declaration permits the selected OpenShift router, DNS Pods, test SQL,
identity-provider, and telemetry Pods, plus operator-bound Kubernetes addresses.
State namespaces permit no traffic. Reader roles can inspect the named policy
and list the complete policy set. The copied component schema is now 1.15.0.

The complete browser workflow now includes bounded probes in both Gateway
namespaces before and after recovery. It checks fresh allowed connections and
denied access to the other Gateway and the control API. Each denied target must
have a live baseline from the permitted test client. Probe Pods mount no tokens
or Secrets and have a 90-second deadline. The inspection fixture supplies Pod
creation, log reading, and policy observation rights only in its bound namespaces.
These probes have not run on the cluster yet. They do not establish complete
network isolation. An unrelated namespace and live endpoint changes still need
checks. The CNPG fixture now adds its selected database Pod peer to the allocation
profile and the worker. A generated baseline keeps its inspection-role check
separate. Its live isolation workflow remains required. External RDS deployments need explicit operator endpoint declarations;
the default test SQL peer does not permit an arbitrary external server.

The current candidate `5043608` uses STEGO `5516e48`. Regeneration passed in both
modules. Its [application CI](https://github.com/jsell-rh/hypershell-stego/actions/runs/35001051267)
is pending. The endpoint change passed 168 live admission checks, full compiler
CI, and cleanup. See the [endpoint record](allocated-network-endpoints-20260915.json).
Production Gateway isolation remains off. The next workflow change must bind
actual operator addresses in both the admission setup and the generated workers.
The setup now shares the operator's saved Kubernetes endpoint set with the
test Job. Ten bounded installation checks and nine inspection checks passed.
SQL and identity-provider paths still need their declared network rules.

The earlier candidate `101f31d` uses STEGO `e394ab5`, which adds declared namespace, Pod, port,
and protocol peers to the protected allocation policy. The [admission record](allocated-network-peers-admission-20260915.json)
contains 89 passing live checks: 18 allowed and 71 denied. Regeneration and
cleanup passed on jshell. No Pods ran in that check. Full compiler CI passed in
[run 34997668449](https://github.com/jsell-rh/stego/actions/runs/34997668449).

Candidate source `101f31d` passed regeneration in both modules. Its application
checks are tracked in [run 34998079776](https://github.com/jsell-rh/hypershell-stego/actions/runs/34998079776).
The rendered browser workflow passed in 97 seconds. Its 229 console tests,
bundle reproduction, regeneration, and image checks passed. Core acceptance passed in 1360.499 seconds. The manual run skips CNPG and Sandbox.

The inspected Gateway screenshot shows Provisioning. The service-account screen
uses a separate Healthy fixture. This run does not create a Gateway Pod or
prove its network isolation. Production allocation isolation remains off. The
next application change must supply all required paths and prove fresh allowed
and denied connections in the complete Gateway workflow. The external DNS
provider choice remains open.

STEGO `8aedc54` adds controlled updates for intact, owned allocation policies.
The [update admission record](allocated-network-update-admission-20260915.json)
contains 93 passing checks, including approved rule changes, retired-rule denial,
and stale-version rejection. Cleanup and [full compiler CI](https://github.com/jsell-rh/stego/actions/runs/34998601541) passed. This
change is newer than candidate `101f31d` and is not covered by its browser result.

STEGO `5516e48` adds operator-supplied IP endpoint bindings. The same checked
addresses feed admission and the generated allocator. The [endpoint record](allocated-network-endpoints-20260915.json)
contains focused runtime and renderer checks and three Kubernetes expression
type checks. Full compiler CI and all 168 admission request checks passed.
Traffic checks remain required. The current candidate uses the endpoint change. The mechanism does not resolve or
track DNS names.

The following record preserves the initial gap and the earlier checks.

The Gateway workflow does not yet prove network isolation in allocated
namespaces. The initial source review on 2026-09-15 found no NetworkPolicy in the
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

On 2026-09-15, the user selected only operator-approved destinations for
Gateway server egress. Sandbox traffic has
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

STEGO kubernetes-service 1.12.0 checks the complete policy set. Its compiler
revision is `7ebd67831f3faf99b0e84a4962a80bf41e4cb9ac`. Full compiler CI passed
in [run 34982147474](https://github.com/jsell-rh/stego/actions/runs/34982147474).
Hypershell regeneration passed with no drift in both modules. The production
network option remains off until permitted paths are implemented.

The [policy-set record](network-policy-set.json) contains the before and after
Hypershell fixture results. With an extra unlabelled allow-all policy, the old
allocator still wrote all nine resources. The new allocator stops after three
writes, before any binding. A valid set still completes all nine writes. The
fixture now returns bounded, filtered list results instead of empty lists.
Use `--additional-policy` to inject the extra policy in the frozen test copy.
Its expected diagnostic status is 2; `go run` reports status 1 and prints the
program's status 2. The production profile without isolation still reports
the earlier gap. These cases are local TLS API tests.

The jshell admission check passed 59 checks: 18 allowed operations and 41 denied
operations. All three policies passed type checking before their bindings were
installed. The test rejected an extra policy before and after regeneration.
It also checked fixed-policy creation, invalid selectors and allow rules,
foreign ownership, permission limits, changed bindings, and cleanup. No Pods
were created. All 14 installed resources, both temporary allocation namespaces,
and their cluster bindings were absent before the shared Lease was released.
This is admission evidence. Permitted traffic and CNI enforcement still require
the full Gateway workflow.

The [STEGO external DNS review](https://github.com/jsell-rh/stego/blob/main/specs/allocated-network-dns.md)
records the open choice between DNS-aware cluster controls and standard
NetworkPolicy with separate IP-rule updates. This affects external PostgreSQL
and other approved services. No provider is selected or enabled. The active
public workflow continues; it does not close the namespace isolation gate.
