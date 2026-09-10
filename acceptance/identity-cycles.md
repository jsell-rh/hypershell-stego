Identity recovery uses STEGO's `ScanCycle` runtime and generated PostgreSQL
checkpoint storage. A scan record contains its desired input version, last grant
cursor, failure flag, and completion flag. STEGO owns the bounded format, page
checks, error retention, time reserve, and transition validation. Hypershell
supplies grants, current user state, provider actions, and authorization.

The controller regression first failed after 10,000 references: an early provider
failure was lost when a replacement controller completed the final 100 references.
The last pass returned success. It now returns `ErrCycleFailed`, even if no new
provider call failed in that pass. A later full cycle can establish a clean result.
This corrects the controller outcome and retry behavior. No provider error text
is stored in the cycle record.

The private load and save RPCs use the fixed `identity-users-cycle` scope. They
require a configured controller subject with the Gateway identity configuration
grant. A save locks the live Gateway, checks the desired generation and checkpoint
version, validates the retained grant cursor, and preserves an earlier failure
within the same unfinished cycle. Only a completed cycle or a changed desired
source can start without that failure. The API hides old-generation evidence
while it preserves stored history and its checkpoint version.

Gateway grant creation and deletion reset this cycle in the grant transaction.
The reset retains and advances the checkpoint version, including when the data
is already empty. An older pass then receives a conflict. The grant, cycle reset,
and events must all commit together. A failed event rolls back every change.
The source generation covers declared Gateway desired fields. Checkpoint reset
covers grant writes through the application service. Direct SQL mutations do
not implement that application contract.

A cursor update itself changes no public Gateway revision and emits no domain
event. A completed record remains stored. The next full cycle starts at the
beginning. Work timeouts can preserve partial progress and failure evidence;
parent cancellation prevents saving. Failed saves can repeat provider work.
All provider actions must remain safe to repeat.

Deploy the API before the new controller. The controller stops when the cycle
RPC contract is absent. The older raw-checkpoint RPCs and `identity-users` scope
remain available for older callers. The new controller starts its first cycle
at the beginning; it does not adopt an old cursor that lacks failure evidence.
Retain both scopes while old writers can exist. No new database table or schema
migration is required beyond the existing checkpoint migration.

`TestIdentityCycleInvalidatesWithGrantAndEvent` uses PostgreSQL, TLS gRPC, REST,
and generated event delivery. It verifies stored failure across API restart,
rejection of an erased failure, denied callers, grant create and delete, stale
saves, event rollback, changed Gateway generation, and deletion. The existing
controller/API restart test now requires the earlier work-timeout failure to
remain in the completed cycle. The first combined run passed in 29.785 seconds
under race detection.

The separate [grant condition](grant-conditions.md) now reports complete, clean
scans of stored grant references. It is not a general readiness or liveness claim.
Observation-age policy and cross-process provider ownership remain open. The
`ClientReady` condition continues to describe only Gateway identity client
configuration. A checkpoint version rejects stale state writes; it cannot fence
a late provider write. The grant-condition upgrade adds a required resource
revision to cycle writes and an event when the condition changes. Partial cursor
updates without a condition change still preserve the public resource revision.

The broader local run passed all 21 selected application tests in 174.065 seconds
under race detection, with PostgreSQL and Keycloak required. It covered Gateway
creation and owner/event rollback, REST and gRPC access, grants and last-owner
protection, real identity provisioning and login, cleanup, restart, bounded
cursor reads, provider deadlines, conditions during concurrent changes, and
cycle invalidation. Unit tests, contract tests, and static checks also passed.
The full application suite and Kubernetes workflows remain CI gates.

After the API switched to STEGO's shared transition validator, the cycle API,
controller/API restart, and compiler-version tests passed in 28.938 seconds.
The final compiler pin is `635dc4636f1d2ffa808b300a668c2eceaae52ad3`.
Controller and service race tests and `go vet ./...` also passed after that switch.

Pinned regeneration preserved all 90 generated, state, and dependency file hashes.
