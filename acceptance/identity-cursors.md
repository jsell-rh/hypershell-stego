Identity recovery uses STEGO's `ScanFrom` runtime and PostgreSQL cursor reader.
The controller no longer stores a page number or row offset. It stores the last
grant ID whose per-user emitter completed. Each pass reads at most 100 pages of
100 references. A larger inventory continues on a later pass. A complete scan
removes its saved cursor and starts a new full scan on the next cycle.

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
the context limit keeps that grant eligible for the next pass. Successful work
followed by cancellation advances the cursor. Cursor progress does not prove
provider convergence.

The old controller failed `TestUserRecoveryReachesBeyondTenThousandGrants` in
0.030 seconds. It stopped at the 10,000-reference limit. The new test reaches
10,100 references in two bounded passes. Other controller tests cover partial
pages, canceled final users, 64 independent scans, duplicate user references,
and malformed pages before effects.

`TestIdentityReferenceCursorThroughGeneratedRuntime` uses PostgreSQL, TLS gRPC,
and the generated scan runtime. It interrupts the first page after seven items,
restarts the API, resumes from the saved cursor, and reads all 10,106 references
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

Durable progress storage, memory bounds for incomplete Gateway cursors, and
cross-process fencing remain open. Progress is still process-local. A controller
process restart starts a full inventory scan. Repeated restarts can therefore
delay later users. This change removes the fixed inventory limit; it does not
resolve that durable-progress requirement.

After the final per-page duplicate-user check, the real identity-controller
workflow passed again in 36.270 seconds. Real Gateway login from current grants
and independent identity cleanup after restart passed in 38.802 seconds.
The final controller race tests and vet also passed.
