Global role records now follow verified API claims at the application request
boundary. `gateway:creator` and `platform:admin` produce global RoleBinding
records. An empty role set removes both records. Other claims do not produce
global grants. Removing creator status preserves existing Gateway ownership.

REST endpoints run preparation after verification and successful input decoding,
before the domain operation. gRPC services run it after verification and runtime
admission. Unary preparation follows protobuf decoding; stream preparation runs
once before the stream handler. Live watch messages do not repeat preparation.
A preparation failure stops the operation and keeps an application error status.
It is not reported as a bad token.

The preparation transaction resolves the verified issuer and subject, updates
profile fields, compares the two managed roles, changes their global records,
and queues the corresponding events. Any failure rolls back that transaction.
The later domain operation has its own transaction. A denied Gateway request
therefore cannot undo the removal of a global role. Gateway creation still
commits the Gateway, its owner grant, and their events together.

The global records describe the claims from accepted requests. Authorization
still uses the current verified token and stored Gateway grants. It does not
use a stored global record as a substitute for the token. A new request with an
older valid token can still project its older claims. This is not immediate
provider revocation or a monotonic record of token issuance. The pending token
revocation policy remains open.

Global grants have no Gateway ID. REST omits that optional field, and gRPC leaves
the optional field absent. The caller can list and read their own global records.
A Gateway owner, a platform-admin token, or a configured controller cannot read
another user's global records solely because of that status. Existing Gateway
inventory rules remain in effect. API callers cannot create or delete global
grants directly; the verified issuer claims supply them.

The generated runtime delivers global creation and deletion events through the
outbox and grant watch service. Events have the grant ID as their key and omit
the Gateway field. Active replay includes global and Gateway grants that the
caller can read. A deleted global record remains as history, and a later re-grant
gets a new ID. Replay recovers active state; it does not recover missed deletions.

The current installation requires a fresh schema. Apply the installation schema
as specified in the installation guide. The old database-catalog upgrade path is
not supported. The earlier role migrations remain historical evidence only.
Global scope permits an absent Gateway ID. Gateway scope requires an ID. The
current schema enforces the relationship between scope and reference presence.

| Behavior | Evidence |
| --- | --- |
| Claim projection | REST creates two managed global roles and ignores unrelated role claims |
| Removal | REST and gRPC remove absent claims, including an empty claim set |
| Reference shape | REST responses pass the pinned schema; gRPC preserves absent Gateway IDs |
| Access | Another user's global grants remain hidden; direct global grant changes fail |
| Ownership | Creator removal denies new Gateway creation but preserves read and sharing rights on an owned Gateway |
| Stream boundary | An existing stream with an old creator token receives later events without restoring the creator record |
| Concurrent requests | Eight callers create one record per role and one event per change; conflict retries do not duplicate them |
| Atomic failure | A failed event write preserves the old role and profile; a preparation error stops REST and gRPC operations |
| Delivery and restart | The generated process delivers global events and retains IDs after restart |
| Real provider | Browser login uses a fresh Keycloak token after role removal and re-grant |
| Schema | Current schema rejects invalid scope/reference combinations; legacy installation upgrades are not supported |

STEGO supplies optional HTTP preparation and a gRPC registrar wrapper. Independent
Record service tests cover verified identity, failures, cancellation, and both
RPC forms. Hypershell supplies the role names, projection transaction, access
rules, and schema migration. No Hypershell entity or role name was added to STEGO.

The following timings are historical results from the earlier schema. They do
not prove the current installation path. The focused global, grant-discovery,
and role-catalog race checks passed in
18.747 seconds. Migration and concurrent projection checks passed separately
in 1.703 seconds. The corrected sandbox-count workflow and these checks passed
in 6.950 seconds. The full local race suite passed with PostgreSQL and Keycloak
required; the acceptance package took 306.128 seconds. The final HTTP 403
compatibility correction passed focused race checks in 11.156 seconds. Those
checks repeated global changes, migration, concurrency, invalid targets, and
Gateway sharing across transports and restart. Dependency verification and
pinned generation passed.

The broader
goal remains open, including immediate token revocation, user administration,
distributed coordination, remaining workload support, larger REST pages,
and production capacity.

The current cluster check forces eight callers to read the same initial state
before they change roles. Creation and removal each produced seven conflict
retries. All callers completed within the shared 15-second deadline. The check
found one user, two grant records, and four events after both operations. No
record or event was duplicated. The generated transaction executes its callback
once; the test caller retries a complete request with a bounded delay.

The same focused run passed concurrent user registration, concurrent REST user
requests, and rollback on an event write failure. The runner rejects untracked
Go files before it creates a test Job. See
[the source and test evidence](global-role-concurrency-evidence.json) for all
attempts, source hashes, output checks, and cleanup. The final attempt did not
repeat generation. The preceding attempt verified two generations with the
same generator inputs; the final attempt verified unchanged output after tests.

A historical workstation benchmark used 10,000 unrelated users and 100 calls.
It averaged 0.922 ms, 60,914 bytes, and 759 allocations per call on Go 1.26.8,
PostgreSQL 18.6, and an Intel Core Ultra 9 185H. It did not measure production
capacity. Do not run benchmarks or load tests on the workstation. Use CI or a
bounded test in the jshell cluster.
