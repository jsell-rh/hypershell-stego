# CNPG database acceptance on jshell

On 2026-09-14, the bounded CNPG run passed all three required race tests:

- `TestCNPGDatabaseWorkloadAndOfflineDeletion`: 167.99 seconds.
- `TestLocalDatabaseSelectionAndRollback`: 0.21 seconds.
- `TestLocalDatabaseRegistrationThroughRESTAndGRPC`: 7.99 seconds.

The package took 177.253 seconds. Job `stego-cnpg-live-da4c4454/check`
reached Complete. The frozen source archive has SHA-256
`d3a3c3da7d0ce2abe16684ad1c99ff5e3e58e41a94a91c2776ff6612e7e2139d`.
It contains 830 tracked files. Evidence is in
`/tmp/hypershell-cnpg-live-jnyse9wd`. Two compiler runs and the check after
testing produced the same 231 generated, dependency, and state file hashes.
All 231 files also matched the checkout. Later source edits changed only
`.gitignore` and the separate service deployment runner; new CI setup and
evidence files were added after the source freeze.

The fixture uses the generated namespace allocator and a separate database
worker. Each worker gets only its generated identity. The database is
registered through REST against a real managed-cluster ID. gRPC observes its
ready state. The test proves these behaviors:

- The database worker can create CNPG resources in its allocated namespace.
  It cannot create them in the control namespace, delete namespaces, read
  control Secrets, or create RoleBindings.
- SQL uses verified TLS. Plaintext and access to the `postgres` database are
  denied. The application role has no administrative role attributes.
- Data and the application Secret survive database Pod replacement.
  Worker restart repairs configuration changes. Five further reconciliations
  leave the CNPG specification unchanged.
- A Gateway catalog reference blocks database deletion. Removal of that
  reference permits deletion. The cleanup summary survives API restart.
- Denied namespace deletion preserves the cleanup obligation. Permission
  restoration lets the allocator finish removal, then the database worker
  confirms cleanup.

The test driver cannot change ClusterRoles. The operator process removes and
restores only namespace DELETE in the fixed allocator role. It derives the
rules from the frozen generated artifact and checks UID and resource version.
The driver supplies only the required sequence and allow/deny value.

This database test creates a Gateway catalog reference and an empty allocated
namespace. It does not start a Gateway workload or create that Gateway's SQL
database. A narrowly scoped API identity records completion of this reference's
cleanup. The separate [rendered browser workflow](browser-gateway-workload.md)
provides evidence for real Gateway workloads and their SQL cleanup.
This result does not prove external PostgreSQL or Amazon RDS support.

The run used nonprivileged containers. The main Job had a 20-minute deadline,
no retries, one test CPU, and 3 GiB test memory. Its API PostgreSQL fixture had
half a CPU and 512 MiB. The separate CNPG operator and database namespaces had
resource quotas and bounded operator lifetime. SQL probe Pods had 90-second
deadlines. No performance test ran on the workstation.

The test removed its allocated namespaces and bindings without fallback
removal. The host verified removal of all 26 operator resources, generated
permissions, admission policies, and the control namespace. It released the
shared live-test Lease only after cleanup. No test resources remain from this
run.

Earlier attempts remain failed or incomplete evidence. An initial compile
check found a pointer mismatch in the protobuf cluster ID. A later setup used
the control namespace when applying a database Role; another stopped on a TLS
handshake timeout during a read. Each attempt was stopped and cleaned up
before the next run. The namespace selection is corrected. The operator
helper retries only transient reads, at most three times; it never retries a
mutation. None of those incomplete attempts counts as a pass.

The full application CI remains required. The earlier
[run 34876669645](https://github.com/jsell-rh/hypershell-stego/actions/runs/34876669645)
finished with only the service-image job passing. Acceptance, web-console,
and all five provider jobs failed. This focused result does not replace
that failed result or prove all remaining Gateway and Sandbox fixtures.
