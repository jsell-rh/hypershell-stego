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
fixtures remain open. Combined provider and cluster cleanup summaries are now
verified by the contract gate below. Generated SDK type names need review because the
new OpenAPI source paths changed some names. Keep this work on the working
branch until the remaining workflow fixtures and contract checks pass.

## Access checks for local servers and legacy records

The bounded jshell Job `stego-placement-c98fbff8/check` passed four application
checks under race detection on 2026-09-14. The package took 26.881 seconds.
The access check uses two local CNPG servers and a separate legacy cluster.
It proves exact controller grants, denied foreign-cluster writes, and unchanged
records and database events after denial. Gateway cleanup must complete before
the database record can be deleted. The checks repeat after API restart.

Unassigned CNPG records and removed-provider records remain readable as legacy
data. Neither an old unscoped grant nor an exact cluster grant permits writes
to those records. The test restores current database constraints before API
startup. It does not weaken the production registration rules.

The first attempt placed a removed-provider record beside the valid CNPG server.
Gateway creation correctly rejected that ambiguous selection. The revised test
uses a separate legacy cluster so that valid Gateway creation can proceed and
the denied-write checks can run.

Results are in `/tmp/hypershell-cluster-access-eaxnvxfy`. Two generation runs and
the post-test files have matching hashes for all 230 generated and build files.
The Job completed. All 822 frozen source files were checked; only the compiler
pin and two evidence documents changed after the source freeze. Its namespace
and private launch files are absent. This test does not provision a PostgreSQL database
for a Gateway; the separate CNPG workflow covers that behavior.

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

These checks use a Kubernetes test server for provider effects. The later live CNPG gate below
checks the operator with generated namespace permissions. The older workflow
fixtures still need conversion.

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

## Live CNPG Gateway workflow

The complete browser workflow passed on 2026-09-14 with application revision
`7f46379f382de6e2f0f2d262e1f880f65ba8af0a` and STEGO
`158f448545f253cd582035aff3ec51c1302ef6ac`. Two Gateways used one local CNPG
server. Their restricted SQL logins passed verified TLS and cross-database
access denial. Gateway data survived Pod replacement. Worker replacement
preserved database and credential identities, access restrictions, metrics,
logs, and traces.

REST deletion removed one Gateway's SQL database, login, keys, namespace, and
cluster bindings. The other Gateway and shared server remained available.
Browser creation, grants, REST and gRPC access, event delivery, API and console
restart, session behavior, and service credentials also passed.

The test took 409.01 seconds. The Job completed with exit zero. Both generation
passes and post-test output matched all 230 generated and build-record hashes.
All 818 tracked source files matched the frozen copy. Cleanup verified removal
of the test namespaces, CNPG resources, cluster permissions, admission policies,
database volumes, and private launch files. See the
[browser workflow evidence](browser-gateway-workload.md) for details.

External PostgreSQL provisioning, legacy deployment code removal, and conversion
of the full CI suite remain open. Normal shared-server deletion is now covered
by the live browser gate described below.
Keep these changes on the working branch until the remaining checks pass.

## Recovery, deadlines, and cleanup scope

On 2026-09-14, bounded jshell Job `stego-placement-99ccfb3f/check` passed 12
application tests with the race detector in 176.973 seconds. Catalog, database
controller, and cleanup metrics unit packages also passed.

The updated tests register CNPG records with an explicit managed-cluster ID.
They preserve these checks:

- Deleted and retained replay under C and ICU ordering, including pagination,
  exact IDs, provider and cluster fields, API restart, and empty history.
- Recovery pages with no count query, and cursor continuation after an earlier
  row is deleted.
- Provider deadlines, failure observations, reopened cleanup, later recovery,
  and worker shutdown for Gateway, database, and identity controllers.
- Independent cleanup with one blocked resource, retries, private TLS gRPC
  observations, REST deletion, event delivery, metrics, and access denial.

The old cleanup query failed the new scope check: it included an external record
in the local CNPG total. The fixed query combines provider and cluster filters
through STEGO. The three cleanup workflows then passed. See
[cleanup summaries](cleanup-summaries.md) for the scope and grant rules.

Two generation runs and the post-test output matched all 230 generated and
build-record hashes. The frozen source contained 818 tracked files; only the
summary documentation changed during the run. The Job completed. Its namespace
and private launch files were removed. Evidence is in
`/tmp/hypershell-cleanup-scopes-ct37y56i`.

These are control-plane checks with controlled providers. The live CNPG workload
evidence is recorded above. Older deployment workflow fixtures, the console
asset archive, and other outdated contract inputs still prevent a full variant
CI pass.

## Parent deletion waits for cleanup

Database deletion now requires each referenced Gateway to be deleted and its
workload cleanup to be complete. Managed-cluster deletion also requires database
provider cleanup to be complete. The check includes all retained workload
targets. Hypershell selects the links and owners; STEGO supplies the common
query in the same serializable transaction as deletion and event creation.

On 2026-09-14, bounded jshell Job `stego-placement-769ca55c/check` passed four
application tests with the race detector in 41.900 seconds. The new test proved
REST and gRPC denial, unchanged parent records and committed events on denial,
API restart, partial cleanup, reopened cleanup, and successful parent deletion
after the required cleanup. Retained replay, independent cleanup after restart,
and the existing shared-database workflow also passed. The catalog, database
controller, and cleanup metrics unit packages passed.

The old API failed the regression check: it returned HTTP 204 while Gateway
cleanup was pending. The first test attempt failed because the test used an
incorrect version column; that attempt is not a pass. The corrected test compares
the full stored parent record, including its deletion state.

Two generation runs and post-test output matched all 230 generated and build
record hashes. All 821 frozen source files matched the checkout before generated
output was imported. The Job completed, and its namespace and private launch
files were removed. Evidence is in `/tmp/hypershell-parent-cleanup-ovoz71se`.
STEGO revision `2f3a2c06bff4a0a6811757e9a168eb810f57ce53` also passed its full
[compiler CI](https://github.com/jsell-rh/stego/actions/runs/34862811090).

These API checks use controlled providers. The live browser gate also passed
with actual CNPG in bounded jshell Job
`stego-service-20260914-e45e42/service-check`. It used application revision
`62730aac9ab5b7b222da4595c0209ad6c729926d` and the same STEGO revision.

The browser backend denied server deletion while a Gateway was live. Generated
controllers then removed the last Gateway's SQL database, login, credentials,
keys, and namespace. Both Gateway Database resources were absent before server
deletion. Database namespace removal and provider confirmation released the
managed cluster for deletion. The resulting events were delivered. The host
verified removal of the recorded persistent volume.

The full browser test passed in 464.73 seconds. All 821 source files and 230
generated and build-record hashes matched. All test resources and the 26
recorded CNPG installation resources were removed. Evidence is in
`/tmp/stego-service-results.G73rO35y`; see the
[browser workflow record](browser-gateway-workload.md) for the complete scope.
