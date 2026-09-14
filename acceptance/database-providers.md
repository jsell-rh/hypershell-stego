# Database providers and locality

The user changed the database requirement on 2026-09-14. Deployment-backed
PostgreSQL is no longer a supported target. The replacement must support CNPG
and external PostgreSQL. This decision supersedes the earlier deployment
placement requirement; the code removal and replacement checks are not complete.

## Locality

The preferred placement puts each Gateway, its database, and its reconciler
together. At minimum, the database and its reconciliation control plane must
run together. The current implementation uses the preferred placement.
For CNPG, use the Gateway's
managed Kubernetes cluster. Run the database reconciler in that cluster. The
central API does not need to run there.

For external PostgreSQL, the operator must register a server local to the
Gateway's managed cluster. Record that cluster association explicitly. A DNS
name or an API event is not proof of placement. The reconciler must run beside
the database it manages. Do not select a remote server merely because it is the
only database catalog entry in the control plane.

## One logical database per Gateway

Both providers must create one logical database and one login for each Gateway.
The user explicitly requires external PostgreSQL reconciliation to create the
database, user, permissions, and credentials. External mode is not limited to
connections that the operator has already created.

Each login must be able to use only its Gateway's database. Prove successful
access to its own database and denied access to another Gateway's database and
unrelated databases. Use verified TLS and bounded operations. Keep passwords
out of API responses, logs, traces, metrics, and test evidence.

Creation, retries, restart, and cleanup must check current ownership and stored
placement. A missing credential must not silently replace a credential for
existing data. Do not convert or delete old deployment-backed database records
or volumes as part of a schema upgrade.

## Responsibility and evidence

STEGO must supply reusable SQL provisioning, connection security, bounded
execution, controller behavior, telemetry, and Kubernetes allocation. Hypershell
supplies Gateway identity, catalog selection, locality, and lifecycle rules.

Replace the deployment-backed application fixture with the supported providers.
Retain the Gateway creation, atomic owner grant, REST and gRPC access, filtered
lists, denied requests, event delivery, restart, and regeneration checks. Add
database isolation checks with actual PostgreSQL connections. CNPG allocation
must preserve the shared-cluster namespace isolation requirement.

## Database registration and selection checks

The first implementation step requires `cluster_id` when an operator registers
a CNPG or external PostgreSQL server. Gateway creation selects one server in
that cluster. A missing or ambiguous server prevents creation. A Gateway cannot
move to another cluster while it retains its database association. Existing
deployment-backed records remain in storage; new registrations are rejected.

On 2026-09-14, the bounded jshell Job `stego-placement-2478d539/check` passed:

- Catalog, database placement, and Gateway unit checks with the race detector.
- Local CNPG and external server selection, rejected remote selection, and
  rollback after an ambiguous selection.
- REST and gRPC registration, denied requests, and placement after API restart.
- Repeated migration with preserved legacy rows.
- Existing protobuf fields and the three explicit database placement additions.
- The operator-set default release and atomic creation checks.

Two generation runs and the checks produced identical hashes for all 230
generated files and build records. The frozen source matched 812 checkout files.
The Job completed. Its namespace and local private launch files were removed.
Local evidence is in `/tmp/hypershell-local-database-final-a52ya99b`.

This gate covers registration and selection. It does not prove SQL provisioning,
physical network locality, cross-database access denial, or the complete Gateway
workflow. Deployment runtime removal and conversion of the older application
fixtures remain open. The cleanup summary also needs a check that combines
provider and cluster filters. Generated SDK type names need review because the
new OpenAPI source paths changed some names. Keep this work on the working
branch until the supported-provider workflow passes.

## CNPG namespace allocation

The CNPG worker now requires the control namespace and managed-cluster ID.
It checks the generated allocation before it writes a CNPG Cluster. It cannot
create or delete a namespace. The allocator removes a deleted database's
namespace; the CNPG worker confirms cleanup only after the namespace is absent.
The worker rejects the removed deployment provider at startup.

The database allocation profile now grants namespaced CNPG permissions. Its
Gateway worker binding includes the per-Gateway database, role, and key cleanup
operations. The database worker has no Secret access. Separate immutable key
records replace the old single-Gateway namespace identity for shared CNPG.

On 2026-09-14, jshell Job `stego-placement-3db3ed0f/check` passed the full database
controller, namespace adapter, and database worker unit packages with the race
detector. It also passed the five registration, migration, API compatibility,
and default-release checks listed above. Both generation runs and the checks
had identical hashes for 230 generated files and build records. Evidence is in
`/tmp/hypershell-cnpg-allocation-bktshuoq`.

These checks use a Kubernetes test server for provider effects. A live CNPG
operator with the generated namespace permissions remains a required check.
The older workflow fixtures still need conversion.

## Gateway cleanup and runtime evidence

Gateway cleanup now reads the retained database record before provider effects.
The provider checks the database ID, namespace, provider, and local cluster.
Namespace labels cannot select the provider or skip SQL cleanup. A shared CNPG
server remains after a Gateway is deleted. Cleanup waits for the Gateway's SQL
resources; it does not delete the shared server record.

On 2026-09-14, jshell Job `stego-placement-4e6e16cc/check` passed all four affected
unit packages with the race detector: database controller, namespace adapter,
database worker, and Gateway workload. It also passed the five API and placement
checks above and `TestSharedDatabaseCleanupThroughGeneratedRuntime`.

The new application check created two Gateways through REST on one database
registration. It observed the deletion event, restarted the API, and recovered
pending cleanup through the generated controller runtime. The other Gateway and
shared server remained. The test uses a controlled SQL provider. It does not
prove actual PostgreSQL creation, connection isolation, or removal.

The final check used STEGO revision
`b71f580bae99c68f66e8d7186ef288e56d16bcbb`. This revision fixes a race between
parent cancellation and task-context cancellation. Both generated service entry
points include the fix. STEGO's full race suite, module verification, and
vulnerability scan passed in [CI run 34849489853](https://github.com/jsell-rh/stego/actions/runs/34849489853).

All 814 frozen source files matched the checkout before generated files were
collected. Both generation runs, the checks, and the collected files had matching
hashes for 230 generated files and build records. The Job completed, and its
namespace and private launch files were removed. Evidence is in
`/tmp/hypershell-shared-cleanup-e1c0sfvs`.

The first attempt, `stego-placement-d2a85c38/check`, failed because its event
assertion had not consumed the earlier creation event. The corrected test reads
creation and deletion in order. That failed Job and its namespace were removed
before the final run. Do not treat the first attempt as a pass.

The next application gate is a live CNPG operator with generated allocation and
worker permissions, actual per-Gateway SQL state, and database access checks.
External PostgreSQL provisioning, legacy deployment code removal, and conversion
of the full workflow suite remain open. Keep these changes on the working branch
until that application gate passes.
