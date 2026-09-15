The complete public Gateway workflow passed with generated Gateway and state
network policies enabled. Source `ee3d41d` used STEGO `5516e48`. The bounded
jshell run passed in 512.91 seconds under the race detector. All 28 fresh
connection checks passed before and after recovery. The
[workflow record](gateway-network-workflow-20260915.json) retains the source,
results, artifact hashes, earlier failures, and cleanup evidence. Ordinary CI
[35005977361](https://github.com/jsell-rh/hypershell-stego/actions/runs/35005977361)
also passed core acceptance in 1357.772 seconds, plus browser, console, and
image checks. CNPG and Sandbox skipped in that run.

The Gateway declaration permits the selected OpenShift router, DNS Pods, test
PostgreSQL server, identity provider, and telemetry receiver. It also permits
the operator-bound Kubernetes addresses. State namespaces permit no traffic.
Reader roles can inspect the named policy and list the complete policy set.
Common policy generation, validation, ownership checks, and reconciliation
remain in STEGO. Hypershell declares its required destinations.

Both Gateway namespaces passed fresh connections to Kubernetes, PostgreSQL,
the identity provider, and telemetry. Connections to the other Gateway and the
control API timed out. Both Gateways also timed out against a reachable
listener in an unrelated namespace with no ingress NetworkPolicy. This checks
Gateway egress independently of destination ingress restrictions. Each denied destination had a live connection baseline
from the permitted test client. These checks passed before and after controller
restarts and namespace replacement: 28 checks in total. Probe Pods mounted no
token or Secret and had CPU, memory, storage, and time limits. The frozen test
fixture supplied inspection and probe rights only in its bound namespaces.

The same run passed Gateway creation, grants, filtered lists, denied requests,
REST and gRPC access, event delivery, public TLS recovery, certificate rotation,
SQL faults, encrypted credential recovery, and normal deletion. All eight
expected worker instances exported metrics and correlated logs and traces.
Browser service-account creation, token use, revocation, and deletion passed.
Generation hashes matched before and after the test. The wrapper confirmed
that both test namespaces and owned resources were absent and released the shared
Lease. The existing supplied PostgreSQL server stayed available during Gateway
deletion. The fixture later removed that server with its test namespace.

The [address-change preparation](endpoint-change-preparation-20260915.json)
passed generation, drift, and 19 focused checks at source `a39c81f`. It adds one
test endpoint name to the Gateway allocation profile. The operator plan changes
one address in the generated admission variable and preserves all other cluster
fields. The listener fixture uses two distinct Pod addresses. The live workflow
must still connect these inputs, change the policy, restart the workers, and
prove new-address access and old-address denial through recovery. This work has
not yet produced a live address-change result.

The full network gate remains open:

1. Check approved and retired endpoint addresses with fresh connections,
   controller restart, and regeneration.
2. Complete the CNPG workflow with network isolation enabled. Source `38a1d76`
   passed generation, full source verification, and 22 focused preflight and
   workflow checks. Its live restricted workflow is active. The earlier run
   stopped before browser setup and required manual cleanup. The
   [CI policy update](network-ci-update-20260915.json) passed resource identity,
   specification, and admission checks before this run.
3. Declare and test external database destinations. The default SQL Pod peer
   does not permit an arbitrary external server, including RDS.
4. Implement and prove a supported DNS-aware provider. The user approved the
   [provider-independent contract](https://github.com/jsell-rh/stego/blob/main/specs/allocated-network-dns.md).
   Unsupported configurations must fail. Technology Preview features are
   excluded. No DNS-aware provider implementation is enabled yet.

The [policy-set record](network-policy-set.json) and
[endpoint admission record](allocated-network-endpoints-20260915.json) retain
separate compiler and admission evidence. Those checks do not replace traffic
checks. The user deferred the live Kata test; this Gateway result does not
establish Sandbox runtime isolation or full production readiness.
