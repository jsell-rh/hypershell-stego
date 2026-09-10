# Database stream startup

The database controller now uses STEGO's generated `RequireStreamHeaders`.
STEGO reads the initial headers, preserves errors sent before headers, and
requires one exact value for each named capability. Missing, duplicate, and
different values fail. Header checks do not consume the first event when headers
are present.

Hypershell still selects `hypershell-managed-database-delete-tombstones: v1` for
live watch and replay. Retained replay also requires
`hypershell-managed-database-replay: retained-v1`. These names and their meaning
remain domain policy. Missing deletion support is a watch contract error.
Missing replay scope is a scan contract error. Unsupported replay RPCs remain
terminal. Retryable RPC errors retain their status codes.

`TestGeneratedClientPreservesDatabaseWatchFailure` uses the generated TLS client
to check early failures and successful empty streams. The database source tests
require invalid recovery to stop before resource reads or provider work. The
existing raw-client and metadata tests also pass. The generated STEGO tests
cover preservation of the first event and all required-header validation.

The controller race tests passed in 1.144 seconds. Contract race tests passed in
1.433 seconds, and application static checks passed. Generation added one client
helper. All 73 previous generated and dependency file hashes are unchanged.

The real database gate passed in 89.440 seconds. The provider workflow took
64.38 seconds and checked TLS, stored data, password retention, foreign namespace
denial, offline deletion, and cleanup of late resources. Deleted-row replay took
11.79 seconds. Retained live and deleted replay took 12.23 seconds. Both replay
checks used C and ICU ordering and crossed API restart through the generated
runtime.

The complete Gateway gate passed in 206.710 seconds. Deletion before workload
startup took 42.38 seconds. The live Gateway, database, and identity workflow
took 163.28 seconds. It checked owner and viewer access, filtered results, denied
writes, three service-account identities, provider persistence, Pod and database
restart, namespace replacement, offline deletion, and former-cluster cleanup.
The cleanup check reopened a prior confirmation after a late namespace appeared.

These local checks cover the changed database stream consumer and the complete
Gateway workflow. The full PostgreSQL and Keycloak acceptance suite was not
repeated locally for this refactor. CI runs that suite for the committed change.

The [STEGO stream contract](https://github.com/jsell-rh/stego/blob/main/specs/grpc-stream-contracts.md)
records input bounds, context ownership, and validation cost. This change does
not add database generations, field ownership, durable retries, or provider
fencing. Those requirements remain open.
