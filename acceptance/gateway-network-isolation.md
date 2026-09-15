Candidate `ee3d41d` adds an unrelated listener to the direct cluster test.
Its full workflow is running in `stego-service-20260915-734cfd`. The listener
is ready in a separate namespace with no ingress NetworkPolicy or allocator
labels. Direct runs require the listener and 28 connection checks. The fixed
restricted CI installation has no listener and records `independent_egress`
as false. The new denial check has not passed yet.

The complete public Gateway workflow passed with generated Gateway and state
network policies enabled. Source `56a5998` used STEGO `5516e48`. The bounded
jshell run passed in 492.39 seconds under the race detector. The
[workflow record](gateway-network-workflow-20260915.json) retains the source,
results, artifact hashes, earlier failures, and cleanup evidence.

The Gateway declaration permits the selected OpenShift router, DNS Pods, test
PostgreSQL server, identity provider, and telemetry receiver. It also permits
the operator-bound Kubernetes addresses. State namespaces permit no traffic.
Reader roles can inspect the named policy and list the complete policy set.
Common policy generation, validation, ownership checks, and reconciliation
remain in STEGO. Hypershell declares its required destinations.

Both Gateway namespaces passed fresh connections to Kubernetes, PostgreSQL,
the identity provider, and telemetry. Connections to the other Gateway and the
control API timed out. Each denied destination had a live connection baseline
from the permitted test client. These checks passed before and after controller
restarts and namespace replacement: 24 checks in total. Probe Pods mounted no
token or Secret and had CPU, memory, storage, and time limits. The frozen test
fixture supplied inspection and probe rights only in its bound namespaces.

The same run passed Gateway creation, grants, filtered lists, denied requests,
REST and gRPC access, event delivery, public TLS recovery, certificate rotation,
SQL faults, encrypted credential recovery, and normal deletion. All eight
expected worker instances exported metrics and correlated logs and traces.
Browser service-account creation, token use, revocation, and deletion passed.
Generation hashes matched before and after the test. The wrapper confirmed
that its namespace and owned resources were absent and released the shared
Lease. The existing supplied PostgreSQL server stayed available during Gateway
deletion. The fixture later removed that server with its test namespace.

The full network gate remains open:

1. Check Gateway egress denial against an unrelated namespace whose listener
   permits incoming test traffic. The two current denied destinations also
   have ingress controls, so those checks alone do not isolate egress behavior.
2. Check approved and retired endpoint addresses with fresh connections,
   controller restart, and regeneration.
3. Run the complete CNPG workflow with network isolation enabled. Candidate
   `7e23873` passed generation and 12 cleanup boundary checks. Its final
   allocation check uses the saved endpoint bindings. It has not run the live
   CNPG isolation workflow.
4. Declare and test external database destinations. The default SQL Pod peer
   does not permit an arbitrary external server, including RDS.
5. Resolve the [external DNS choice](https://github.com/jsell-rh/stego/blob/main/specs/allocated-network-dns.md).
   No DNS-aware provider is selected or enabled.

The [policy-set record](network-policy-set.json) and
[endpoint admission record](allocated-network-endpoints-20260915.json) retain
separate compiler and admission evidence. Those checks do not replace traffic
checks. The user deferred the live Kata test; this Gateway result does not
establish Sandbox runtime isolation or full production readiness.
