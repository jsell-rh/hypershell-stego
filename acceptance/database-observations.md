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

Database deletion also needs authoritative retained state. The current controller
passes deletion-event data directly to the provider. Durable cleanup ownership,
conditional completion, and cross-process fencing remain separate requirements.

## Revision contract

The pinned compiler is `25ed7ff44868c90eb892150e2ef36b36cf78f663`.
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
unchanged. Authoritative deletion reads, durable cleanup completion, per-subject
field authority, cross-process fencing, and production capacity remain open.
