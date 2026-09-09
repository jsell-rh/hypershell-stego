# Durable database cleanup

ManagedDatabase declares `cleanup_owners: [provider]`. STEGO supplies the storage
metadata, deletion trigger, conditional observation, migration, and gRPC response
metadata. Hypershell supplies the provider action, controller access policy, and
private protobuf operation. The compiler requirements are `postgres-adapter`
3.8.0 and `grpc-application` 1.6.0.

Deletion records the provider as pending in the same row update. Before provider
work, the controller reads current retained state with its revision and cleanup
observation. It records success only after the provider confirms absence. The
private `ObserveDatabaseCleanup` RPC checks a configured controller subject, the
`provider` owner, an exact cleanup grant, and the exact revision. Its transaction commits the observation
and deletion notice together. A denied or stale request cannot change either.

The notice requests another current-state check. The controller avoids writing
an unchanged result. It continues periodic provider checks after a confirmed
success. A later pending or failed provider attempt clears the confirmation. A
conflict requires a fresh read and another provider attempt. This preserves
repair when an external action finishes late.

The public resource messages stay unchanged. Ordinary reads still return 404
after deletion. Privileged retained reads expose the bounded cleanup metadata.
A completion does not permit physical deletion or ID reuse. It does not remove
the resource from recovery scans.

## Checks

`TestDatabaseCleanupObservationIsAtomicAndSurvivesRestart` uses REST creation and
deletion, TLS gRPC reads and cleanup writes, the generated event runtime, and API
restart. It checks live-resource refusal, denied callers and owners, missing and
malformed revisions, stale observations, event rollback, durable completion,
reopening cleanup, and invalidation after retained data changes.

Controller tests check continued observation after success, reopening after a
late effect, stable-state write suppression, and fresh provider work after a
conflict. Generated storage tests cover independent owners, concurrent writes,
invalid metadata, owner addition, rejected removal, repeated migration, and
rollback of state and events.

The real database workflow checks a DELETE denied by Kubernetes before cleanup
succeeds. It then stops the controller, creates an owned namespace after cleanup
was confirmed, and restarts recovery. A test finalizer keeps that namespace
present until the test observes durable pending state. Removing the finalizer
allows another absence confirmation. The test also retains its TLS, persisted
data, credential, restart, foreign-namespace, and offline-deletion checks.

The initial static cleanup checks passed with race detection. The following
measurements predate target history and cleanup grants. The
application cases completed in 29.975 seconds. The real database workflow passed
in 88.550 seconds, including the late-effect case and deletion replay under both
tested collations.

Generation from the pinned remote compiler reproduced all 66 tested code and
dependency files. Module verification and `go vet ./...` also passed.

The complete Gateway workflow passed in 224.660 seconds. It checks access rules,
identity integration, persisted provider data, Pod and database restart,
namespace replacement, stable keys, and offline deletion with cleanup events.

The full application suite passed with race detection, PostgreSQL, and Keycloak
required. The acceptance package completed in 556.217 seconds.

## Limits

This contract records cleanup observations for the database provider. Gateway
identity and workload cleanup also use the common contract. Cleanup writes now
require exact grants. Other controller operations and external provider
credentials still need separate permissions. There is no cross-process fence, safe
history purge, owner retirement protocol, completion history, or production
cleanup capacity guarantee. Late effects can occur after a recorded success.
Periodic recovery remains necessary.

Cleanup observation writes also require [explicit grants](cleanup-permissions.md)
for the verified issuer, subject, resource, operation, and target.
