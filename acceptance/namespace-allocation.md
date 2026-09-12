# Namespace allocation adoption

The common runtime is in STEGO. The Hypershell adapter in
`internal/namespaceallocation` maps current Gateway and ManagedDatabase records
to the `gateway` and `database` profiles. It shares the existing watch and
recovery readers. STEGO supplies the queue, retries, shutdown, logs, metrics,
and traces.

The adapter rejects missing or invalid resource state. Gateway allocation also
requires a recorded workload cleanup target. A move or deletion removes the
old cluster's allocation. A missing API row does not authorize deletion.
Deployment database allocation requires a recorded cluster on an authorized
retained read. A database assigned to another cluster causes no allocation or
deletion. Missing or malformed placement is an error. CNPG records do not enter
the deployment allocation path.

The adapter now has a generated `namespace-allocation` worker. The application
profiles grant namespaced roles to the database and Gateway workers. Their
remaining cluster role only permits Namespace reads. The browser check uses
seven Deployments. This adoption change is not yet accepted: the full workflow
must pass with these permissions.

## Recorded database placement

Each new deployment database records its Gateway's managed-cluster ID in the
same transaction as the database, Gateway, owner grant, and events. The public
REST and protobuf response shapes do not change. The authorized retained
ManagedDatabase read returns the private `cluster-v1` metadata contract.
Ordinary reads and denied retained reads do not return that metadata.

A normal database patch cannot set or change placement. A Gateway move returns
REST `409` or gRPC `AlreadyExists` when its database uses the deployment
provider. A patch that keeps the same cluster succeeds. Shared CNPG placement
keeps its prior behavior. No database migration workflow exists yet.

Apply `migrations/000009_database_cluster_placement.sql` before the API starts.
It adds a nullable cluster reference and an index. It can run again. It does
not infer the location of existing databases from a Gateway that could have
moved. Existing records and standalone catalog creations remain unassigned;
the namespace allocator rejects them. Operators must verify their actual
location before assignment. A repair command is not yet supplied.

A live deployment database prevents deletion of its managed cluster, even
after Gateway deletion. Database deletion retains the cluster reference for
cleanup. The migration and domain rules do not replace Kubernetes permissions
or cluster-scoped controller authorization.

## Required application gate

The implementation connects the generated worker, fixed profiles, and scoped
roles. Resource workers observe allocation state. The Gateway worker writes an
immutable public ConfigMap; the allocator retains its fingerprint and Gateway
ID on the database Namespace. Keys cannot be used before that step completes.
Test code no longer creates workload namespaces, quotas, or role bindings.

Run the real
Gateway workflow with REST and gRPC access checks, event delivery, worker and
application restarts, key-loss checks, and byte-identical regeneration. Prove
that each worker is denied access to a foreign namespace. Keep this gate open
until the rendered application passes those checks.

## Checked source

The bounded jshell Job passed the adapter tests under race detection in
1.023 seconds. The database controller tests passed in 1.211 seconds, and the
Gateway workload tests passed in 11.429 seconds. The tests cover denied or
missing state, invalid identity, recorded cleanup targets, Gateway moves,
retained database reads, placement rejection, and cleanup errors.

Results are in `/tmp/hypershell-allocation-adapter-d9egw20z`. Source hashes match
the checked files. The Job reached `Complete`, and its namespace was removed.
Its generated output matches all 225 hashes from the separate
[application check](worker-deployment.md#compiler-update-check).

## Recorded placement check

The final bounded jshell check passed on 2026-09-12 UTC. Eight application
checks passed under race detection in 44.287 seconds. They cover REST and gRPC
creation and reads, denied private reads, move rejection, unchanged invalid-ID
errors, transaction rollback, concurrent creation, retained deletion state,
restart, migration, shared-database moves, and controller write grants. The
placement workflow also checks owner grants, filtered lists, and event delivery.

The placement parser, allocation adapter, database controller, Gateway workload
controller, and Gateway domain tests passed under race detection. Their package
times were 1.011, 1.021, 1.207, 11.457, and 1.018 seconds. The Job had a
twenty-minute deadline, 1.5 CPUs, 3584 MiB of memory, and 8 GiB of temporary
storage across its test container and PostgreSQL sidecar. PostgreSQL used
verified TLS.

Results are in `/tmp/hypershell-cluster-placement-final-i77z01_d`. All 465
archived source and configuration hashes match the checked source. All 225
generated-file hashes match both generation passes, the post-test files, and
the checkout. The Job reached `Complete`. Its namespace and private fixture
files are absent. It created no cluster RBAC objects.

An earlier source check also passed in 22.992 seconds. Its results are
`/tmp/hypershell-cluster-placement-u9vb3cba`; its namespace is absent. The final
check adds invalid-ID compatibility and the two existing shared-database move
tests. These checks do not deploy the namespace allocator. The application
gate above remains open.

## Cluster access rules

A deployment database worker must set `HYPERSHELL_MANAGED_CLUSTER_ID` to its
canonical managed-cluster ID. It reads retained placement before work. Missing
or invalid placement is an error; a different cluster causes no provider or API
write. The Gateway worker checks database placement and deletion state before
provisioning. It checks placement before it removes a database record.

The API uses the stored cluster as the exact target for these grants:

- `observe.provider` in `HYPERSHELL_CONTROLLER_WRITE_GRANTS` permits database
  observations. Each observation also requires the resource version.
- `cleanup.provider` in `HYPERSHELL_CLEANUP_GRANTS` permits cleanup observations
  and the cleanup summary for that cluster.
- `cleanup.record` in `HYPERSHELL_CLEANUP_GRANTS` permits the Gateway worker to
  delete its unused database record. Live Gateway references still prevent it.

The resource is `ManagedDatabase` for all three grants. The target for the
shared CNPG provider is `cnpg`; its worker must leave the managed-cluster ID
empty. Old empty cleanup targets and the old `deployment` observation target
provide no fallback. A controller's `platform:admin` role cannot bypass these
rules. Controller subjects cannot create catalog records or change cluster,
release, or network configuration.

The private cleanup-summary request now includes `cluster_id` for deployment
scope. The response confirms that scope. STEGO supplies the bounded aggregate
query; the domain selects and authorizes its filter. The client checks the
returned scope before it supplies a metric sample. No database record IDs enter
the metric sample.

Apply migration `000010_database_provider_placement.sql` after migration 000009.
It prevents a CNPG row from acquiring a deployment-cluster scope. Deploy the
new API and exact grants before the new workers. Verify and record legacy
placement before enabling allocation for existing deployment databases. Public
REST and protobuf resource shapes do not change.

## Cluster access check attempts

The first bounded jshell check passed all six selected controller packages
under race detection. Fifteen application checks passed. Two application
checks failed because their fixtures still used the old settings: the new
cluster test selected shared CNPG mode, and the replay test used a controller
to create catalog records. The corrected fixtures select deployment mode and
use an operator for catalog creation. The run is a failure, not a pass.

Results are in `/tmp/hypershell-cluster-grants-n0772p6g`. The Job reached
`Failed`; its logs and generated files were collected. Its namespace and
private fixture files are absent. Both generation passes match. The test
command stopped before the post-test hash check.

## Cluster access application check

The corrected check passed on 2026-09-12 UTC. All nine selected application
checks passed under race detection in 363.771 seconds. The two-cluster check
passed in 8.50 seconds. It verifies exact observation, cleanup, and record-delete
grants; denies foreign, unassigned, and old unscoped targets; checks that denied
writes produce no database events; and repeats access checks after API restart.
It also denies controller catalog creation and cluster-credential-reference
changes, even when the controller has the platform administrator role.

Delete and retained replay passed with both tested database collations. Network
and placement workflows, recovery pagination, and diagnostic privacy also
passed. The six-Deployment browser workflow passed in 310.21 seconds. It used
the new cluster setting on the generated database worker. The actual OpenShell
Gateway passed requests and provider-data recovery after Pod replacement.
All three workers were replaced and exported metrics and correlated logs and
traces before and after replacement. API, console, and provisioner replacement,
service-account use, and confirmed identity-provider sign-out passed.

Results are in `/tmp/stego-service-results.ozNoPlD4`. The fixed source is
`/tmp/hypershell-cluster-worker-mux9pcw9/application`. All 654 application archive
files match that source. All 225 generated, state, and dependency hashes match
both generation passes, the post-test files, and the checkout. The only source
change after the freeze is this evidence document. Input-manifest and console
isolation checks passed in 1.068 seconds. The final account-deletion screenshot
was reviewed. The Job reached `Complete` with exit zero. The test and workload
namespaces, owned cluster RBAC objects, and private fixture files are absent.
Their removal was verified.

This check uses the existing bounded browser workload profile. It does not
deploy the namespace allocator or remove broad worker namespace permissions.
The namespace-allocation application gate remains open.

## Allocator adoption status

Set `HYPERSHELL_CONTROL_NAMESPACE` on the namespace allocator, deployment
database worker, and Gateway worker. The value must name their control
namespace. It selects the generated allocation identity. The generated worker
roles do not permit the old direct namespace-write path. Direct provider
construction without this setting remains available to the existing isolated
provider tests; it is not the new deployment path.

The current profiles cover deployment databases and the default Gateway
workload. Shared CNPG allocation and the separate Sandbox namespace still need
application checks and scoped profiles. Allocated Gateway mode rejects a
separate Sandbox runtime class until that support exists. Existing namespaces
without the allocation identity are not adopted. No migration or repair command
is supplied by this change.

The initial generation check passed both generation passes, the adapter race
tests, and the new worker entry-point compile check. Results are in
`/tmp/hypershell-allocator-generation-krf_i27s`. A status request had a TLS
handshake timeout. The same Job was observed again and its result was collected;
it was not restarted. The Job reached `Complete`, and its namespace and private
fixture files are absent.

The first application attempt uses fixed source
`/tmp/hypershell-allocated-workflow-4tkrq73k/application` and results directory
`/tmp/stego-service-results.RtCsac91`. Its contract checks and provider race tests
passed. All seven Deployments started. The allocator created four workload
namespaces and recorded both database key fingerprints. Both database Pods
became ready. OpenShell startup failed because the identity-provider fixture
still selected the old test namespace label. The fixture source now selects
the allocator marker and Gateway profile. That correction needs a fresh run.

The final test source also checks seven Deployments, admission through server
dry-run requests, and REST deletion through namespace removal and stored cleanup
observations. These added checks are not yet proved. Collection of the current
Job and the next run require a refresh of the expired local jshell login. Do not
report this attempt as a pass or its cluster resources as removed.
