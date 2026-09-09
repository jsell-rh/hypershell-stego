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

Stop the old API before the schema update. Apply the role catalog migration and
then `migrations/000005_global_roles.sql` with the application database settings:

```sh
psql --set=ON_ERROR_STOP=1 --file=migrations/000004_role_catalog.sql
psql --set=ON_ERROR_STOP=1 --file=migrations/000005_global_roles.sql
```

The second migration permits a NULL Gateway ID only for global scope. Gateway
scope requires an ID. It preserves the existing Gateway grant key and adds a
separate live global key on role and user. The generated schema expresses the
optional reference. The external domain migration enforces the relationship
between scope and reference presence. The application does not run migrations.

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
| Migration | Repeated migration preserves existing Gateway grants and rejects invalid scope/reference combinations |

STEGO supplies optional HTTP preparation and a gRPC registrar wrapper. Independent
Record service tests cover verified identity, failures, cancellation, and both
RPC forms. Hypershell supplies the role names, projection transaction, access
rules, and schema migration. No Hypershell entity or role name was added to STEGO.

The focused global, grant-discovery, and role-catalog race checks passed in
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
distributed coordination, workload deployment, sparse fields, larger REST pages,
and production capacity.

A local benchmark prepared unchanged claims with 10,000 unrelated users and
global grants. Across 100 calls, it averaged 0.922 ms, 60,914 bytes, and 759
allocations per call. It used Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9
185H. The measurement includes the transaction, identity lookup, role lookup,
and comparison of current records. It excludes transport, token verification,
Keycloak, initial registration, role changes, and concurrent load. Run
`go test -mod=readonly -run '^$' -bench '^BenchmarkGlobalRolePreparation$' -benchtime=100x ./acceptance`
with the PostgreSQL test settings. This does not establish production capacity.
