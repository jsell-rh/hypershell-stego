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

This adapter is not yet connected to a worker Deployment. The existing Gateway
workflow still uses the resource workers' namespace permissions. Tests of the
adapter do not prove that those permissions can be removed.

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

Require the database worker to select its recorded managed cluster. Require
the Gateway worker to check that its deployment database has the same
placement. Limit database observation and cleanup grants by cluster; a client
filter alone does not enforce this rule.

Connect the adapter to a generated worker. Declare fixed namespace profiles
and scoped roles. Replace resource-worker namespace
writes with the generated allocation observations. Use an immutable public
record to retain the Gateway key fingerprint through the allocator.

Remove test-created workload namespaces, quotas, and bindings. Then run the real
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
