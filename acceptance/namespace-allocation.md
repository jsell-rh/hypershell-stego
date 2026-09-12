# Namespace allocation adoption

The common runtime is in STEGO. The Hypershell adapter in
`internal/namespaceallocation` maps current Gateway and ManagedDatabase records
to the `gateway` and `database` profiles. It shares the existing watch and
recovery readers. STEGO supplies the queue, retries, shutdown, logs, metrics,
and traces.

The adapter rejects missing or invalid resource state. Gateway allocation also
requires a recorded workload cleanup target. A move or deletion removes the
old cluster's allocation. A missing API row does not authorize deletion.
Deployment database allocation requires an explicit placement check. CNPG
records do not enter the deployment allocation path.

This adapter is not yet connected to a worker Deployment. The existing Gateway
workflow still uses the resource workers' namespace permissions. Tests of the
adapter do not prove that those permissions can be removed.

## Placement decision

A deployment database currently has no recorded managed-cluster ID. Each
cluster must be able to distinguish its databases before it can allocate,
change, or delete their namespaces.

The proposed choice is to place each deployment database in its Gateway's
managed cluster and record that cluster in the control plane at creation.
Database creation, placement, Gateway creation, the owner grant, and their
events must use the existing transaction. A database placement must not change
through a normal update. A Gateway move that needs database migration must fail
until an explicit migration workflow exists.

The alternative is a separate shared database cluster. That choice requires a
separate database allocator identity, private network access from Gateway
clusters, and a defined way to deliver connection credentials to each Gateway.
It must preserve verified TLS and the durable encryption-key checks.

## Required application gate

After the placement decision, connect the adapter to a generated worker. Declare
fixed namespace profiles and scoped roles. Replace resource-worker namespace
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
