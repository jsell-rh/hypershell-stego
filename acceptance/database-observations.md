# Database observations

ManagedDatabase now uses STEGO's generated resource revision contract. Controller
writes require the revision read before provider work. The API checks it and
inserts the event in one transaction. This closes the stale database commit
identified below. Desired generations and field ownership remain separate work.

## Baseline failure

A temporary PostgreSQL probe tested commit
`371bf78` on 2026-09-09. It used the normal catalog service and a configured
control-plane subject:

1. A platform admin created a deployment database named `observed`.
2. The controller read that record.
3. The admin changed its name to `changed`.
4. The controller submitted `ready` from its earlier observation.
5. The probe required rejection of that stale observation.

The probe failed in 0.14 seconds. The API accepted the write and returned name
`changed` with status `ready`. A serializable transaction for the later write
cannot establish which desired state the controller read. The probe did not run
an external database provider; it isolates the missing commit precondition. It
ran in a temporary checkout and was removed after execution.

The next application gate must use the existing generated revision and generation
contracts for database observations. It must test a change during provider work,
owner and controller authority, event rollback, current status through REST and
gRPC, restart, and regeneration. A fresh observation must confirm an unchanged
status for a new generation. Periodic provider checks must continue after ready.

At the baseline, database deletion also passed deletion-event data directly to
the provider. The retained-read change below addresses that gap. Durable cleanup
ownership, conditional completion, and cross-process fencing remain separate
requirements.

## Revision contract

The compiler revision is recorded in [the compiler pin](../.stego/compiler-revision).
`grpc-application` 1.4.0 generates `transport.SetResourceVersion` and
`client.ObservedResourceVersion`. The database Get method returns its revision
in response metadata. The controller captures that metadata with the returned
record before calling the provider. It uses the existing `WithResourceVersion`
helper on its later write. Public protobuf message shapes stay unchanged.

A missing precondition returns gRPC `FailedPrecondition`; malformed metadata
returns `InvalidArgument`. An old revision returns `Aborted`. Only configured
controller subjects can use the conditional catalog path. A controller patch
through ordinary REST returns HTTP 428. Existing platform-admin catalog updates
remain available. A valid revision does not grant access.

The controller rejects missing or invalid response revisions before provider
work. After a conflict it must read current state and repeat the observation.
Live provider selection also uses the current record, not the event hint.
Provider checks still run after ready. A revision check cannot undo an external
action that was already in progress.

Enable the new generated revision migration before this API starts. Replace all
API instances that can accept controller writes before starting the updated
controllers. An old API instance can ignore an unknown request header. New client
code alone cannot enforce a precondition inside that old server. Use separate
migration and application roles as specified in STEGO's resource revision
contract. Database IDs and deletion history are now protected by the generated
trigger. Empty replay tests use a separate database instead of purging history.

## Validation

`TestDatabaseRejectsOldObservationAcrossRESTGRPCAndRestart` covers a REST desired
change, gRPC revision headers, rejected stale and missing preconditions, malformed
and duplicate metadata, denied callers, REST bypass denial, event rollback,
restart, successful event delivery, and retained deletion. It also checks that a
denied read does not return revision metadata. The revision, placement, and replay
checks passed with race detection in 22.883 seconds.

Controller tests check that invalid metadata stops provider work and that a
conflict causes another provider observation before success. They also check
provider selection against current state. The real database workflow passed in
82.692 seconds, including TLS, persisted data, stable credentials, foreign
namespace denial, offline cleanup, and replay under both tested collations.

The real Gateway workflow passed in 235.943 seconds after the current-provider
selection change. It covers database and OIDC integration, owner and viewer
access, denied requests, service accounts, persisted provider data, Pod and
database restart, namespace replacement, stable keys, and offline deletion.

The full application suite passed with race detection and both PostgreSQL and
Keycloak required. The acceptance package completed in 543.946 seconds. Module
verification and `go vet ./...` also passed. Generation from the pinned remote
compiler reproduced all 63 tested code and dependency files.

## Remaining requirements

Database status still lacks desired-generation and observed-generation tracking.
An existing status can describe earlier inputs until the next provider check.
Platform-admin status writes also remain part of the existing catalog contract.
The `connection_secret` ownership choice is pending: the reference API accepts an
admin value, while the deployment controller writes the provider result. The
revision check covers changes to this field without deciding that ownership.

The next step must define the observation fields, apply generation-aware
presentation and search, and require fresh confirmation even when status text is
unchanged. The provider now has [durable cleanup observations](database-cleanup.md).
Cleanup for other resources, per-subject field authority, cross-process fencing,
and production capacity remain open.

## Authoritative deletion reads

A regression test on `2e45575` sent a deletion event while the current database
record was live. It failed: the event alone reached the provider. This test now
requires a current retained read and rejects deletion without current intent.

The compiler now generates `storage.RetainedReader.GetRetained` for versioned
resources. The query reads one exact ID, including deletion state and revision,
without a list count. The gRPC helpers request `retained-v1` read mode and return
explicit deletion metadata. This requires `postgres-adapter` 3.7.0 and
`grpc-application` 1.5.0. Only configured controller subjects can use this
mode in the database API. Ordinary REST and gRPC reads keep their existing
visibility. Missing and denied reads do not return deletion evidence.

The database controller now treats all event data as hints. Before each provider
action, it reads current retained state and checks the returned ID, revision, and
deletion state. A live record cannot authorize deletion; it selects normal
live-state reconciliation even after a delete-type hint. A deleted record causes
cleanup even when an older live event triggered the pass. Provider
selection and cleanup parameters come from the retained read. Each retry reads
again. Failed reads and old servers with missing metadata stop provider work.

`TestDatabaseRetainedReadAndCleanupAfterRestart` checks REST deletion, privileged
and denied retained reads over TLS gRPC, strict metadata, ordinary-read denial
after deletion, revision persistence, and controller recovery after API restart.
It changes the event fields at the client boundary and verifies that the provider
receives current retained data through the generated controller runtime. Unit
tests also cover a false deletion event, failed reads, wrong IDs, and cleanup
retry. The provider in this regression is a recorder; the real database workflow
separately checks Kubernetes cleanup.

The focused retained-read, revision, and replay tests passed with race detection
in 21.980 seconds. The real database workflow passed in 85.642 seconds. It checks
TLS, persisted data, stable credentials, foreign namespace denial, offline
cleanup, and replay under both tested collations. The pinned remote compiler
reproduced all 63 tested code and dependency files.

The complete Gateway workflow passed in 235.120 seconds. It checks database and
OIDC integration, access rules, service accounts, persisted provider data, Pod and
database restart, namespace replacement, stable keys, and offline deletion.

The full application suite passed with race detection, PostgreSQL, and Keycloak
required. Its acceptance package completed in 546.600 seconds. Module verification
and `go vet ./...` also passed.

The later [cleanup contract](database-cleanup.md) records the provider owner and
conditional observations. It does not fence another controller process or undo an external action already
in progress. Periodic recovery remains necessary. The schema prevents ordinary
ID reuse and reversal of deletion; database restore and administrator changes
remain outside this contract. Replace all old controllers after the API upgrade
before claiming that every cleanup action uses current retained evidence.

The database controller now queues validated IDs through STEGO's keyed runtime.
Event types do not select provider actions. This permits repeated hints for one
resource to share one queue key while preserving current-state authority. Missing
retained state remains an error for all event types and permits no provider work.
See the [database scheduling evidence](database-scheduling.md).
