Identity recovery uses STEGO's `ScanCheckpointed` runtime, PostgreSQL cursor
reader, and `CheckpointStore`.
The controller no longer stores a page number or row offset. It stores the last
grant ID whose per-user emitter completed. Each pass reads at most 100 pages of
100 references. A larger inventory continues on a later pass. A complete scan
resets its saved cursor and starts a new full scan on the next cycle. The
checkpoint version remains stored so that an older writer cannot restore old
progress.

The new private `ScanGatewayIdentityUsers` RPC accepts a Gateway ID, grant cursor,
and page size. It echoes the Gateway and cursor and returns grant/user pairs
with a continuation flag. Only configured controller identities can call it.
The service rejects invalid IDs and page sizes before storage access. Storage
returns live and retained deleted grants in database ID order. The query selects
only grant and user IDs. It uses no count or row offset.

The controller validates the whole page before provider work. It rejects a
missing response, wrong scope, duplicate cursors, empty user IDs, and invalid
continuation. An unsupported new RPC stops the controller with a scan-contract
error. Other RPC errors keep their status. Deploy the new API before the new
controller. The older page-number RPC remains for existing clients; its old
limits and count query remain. The new controller does not use that RPC.

Each provider update still requires fresh user identity and grant state. Repeated
references to one user within a page cause one state read and provider call.
An ordinary per-user failure does not block later users. The first such error is
returned, and a later full scan retries that user. A failed call that reaches
the context limit keeps that grant eligible for the next pass. The runtime reserves two seconds to save progress after the work deadline.
Parent cancellation prevents that save and can repeat the unsaved prefix.
Cursor progress does not prove provider convergence.

The old controller failed `TestUserRecoveryReachesBeyondTenThousandGrants` in
0.030 seconds. It stopped at the 10,000-reference limit. The new test reaches
10,100 references in two bounded passes. Other controller tests cover partial
pages, canceled final users, 64 independent scans, duplicate user references,
and malformed pages before effects.

`TestIdentityReferenceCursorThroughGeneratedRuntime` uses PostgreSQL, TLS gRPC,
and the generated scan runtime. It saves the first seven items at a page limit,
restarts the API, loads the saved cursor from PostgreSQL, and reads all 10,106 references
without loss or repeat. The test includes retained deletion, duplicate user
references, denied callers, and invalid requests. This is an inventory test;
it does not perform 10,106 external provider writes.

`TestIdentityReferenceCursorUsesBoundedReads` requires exactly two SELECT reads
per page and no count query. It inserts a retained reference before the saved
cursor. The current scan stays stable; the next full scan finds the insertion.
Denied and invalid calls perform no storage reads. Current-role and deleted-user
tests also pass. A new full scan remains necessary for concurrent changes before
a cursor; this protocol does not claim a cross-page snapshot.

The first eight selected PostgreSQL/Keycloak acceptance tests passed in 57.873
seconds. They include Gateway creation, access rules, atomic grants, events,
restart, identity provisioning, and cleanup. The final insertion and CLI version
checks passed in 2.139 seconds. Unit and contract tests and vet passed. The full
application suite and Kubernetes workflows were not repeated for this change.

Three local 200 ms benchmark samples used Go 1.26.8, Linux amd64, Intel Core
Ultra 9 185H, PostgreSQL 18.6, and 10,105 seeded grant references plus the owner.
The old page-100 inventory read took 3.632–3.876 ms, 152,221–152,713 bytes,
and 2487–2489 allocations. The new cursor read after item 9900 took
0.764–0.917 ms, 149,888–150,144 bytes, and 1490–1491 allocations. Both returned
100 references. These measurements include storage transactions and domain
mapping. They exclude gRPC, provider calls, and controller scheduling. They do
not establish a production capacity or recovery target.

Durable progress now resides in generated PostgreSQL storage. The controller
has no cursor map. One fixed scope, `identity-users`, permits one checkpoint row
per Gateway. The private load and save RPCs require the exact Gateway identity
configuration grant. Saves lock the live Gateway and reject a cursor outside
its retained grant source. Deletion prevents later saves. A cursor write does
not change the public Gateway revision or emit a domain event.

`TestUserScanSurvivesControllerReplacement` failed before this change. A new
controller repeated the first 10,000 references and could not reach the tail in
its next pass. It now loads the checkpoint and completes the 10,100-reference
fixture. `TestIdentityCheckpointSurvivesAPIAndControllerRestart` uses the real
API, PostgreSQL, controller runtime, and a recording provider. It reaches a work
timeout, saves progress, replaces the API and controller, and checks a changed
grant before the next provider action. Saved work is not repeated. The test also
checks that saves stop after deletion. The direct RPC scan test checks that
cursor writes preserve the public revision. The controller can also publish an
identity condition, which changes the resource revision.

Apply `out/storage/migrations/000006_scan_checkpoints.sql` before starting the
new API when migrations run externally. Startup rejects missing or invalid
checkpoint key columns. Deploy the API before the new identity controller;
there is no fallback to process-local progress.

Cross-process provider ownership, durable retry scheduling, safe history
retirement, and production capacity evidence remain open. Identity client
conditions are now covered in [identity conditions](identity-conditions.md). A
checkpoint version check does not prevent concurrent provider calls. Retain
checkpoint rows while older writers can exist, and do not reuse resource IDs.

After the final per-page duplicate-user check, the real identity-controller
workflow passed again in 36.270 seconds. Real Gateway login from current grants
and independent identity cleanup after restart passed in 38.802 seconds.
The final controller race tests and vet also passed.

The durable-checkpoint update passed 12 selected application tests in 143.866
seconds with the race detector and required PostgreSQL and Keycloak fixtures.
These tests cover Gateway/owner/event commit and rollback, REST and gRPC,
identity provisioning and cleanup, stored-grant login, provider deadline
recovery, both cursor restart tests, bounded reads, and the clean compiler
version record. All unit and contract tests and `go vet` also passed.

An earlier broad local run used a six-minute limit and did not complete. It
also rejected the temporary compiler build before the clean pin was applied.
The final selected tests use compiler `4bf903c557c9c6a330bc0889ef1ec30bb018b41d`.
The complete application suite and Kubernetes jobs remain remote CI gates.

The durable-checkpoint commit `54dc1eebd62440b4de1ef5a50c84e8403113e4d5`
passed all six remote CI jobs in run
[34513823967](https://github.com/jsell-rh/hypershell-stego/actions/runs/34513823967).
These include the full application suite and the five Kubernetes workflow jobs.
