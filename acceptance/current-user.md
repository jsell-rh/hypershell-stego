The Gateway sharing test now obtains the recipient user ID through the
application API. The recipient signs in, calls `GET /api/hypershell/v1/users/me`,
and supplies the returned ID to the owner. The owner finds the viewer role
through the role catalog and creates the grant. The real Keycloak browser test
uses these requests. It has no direct application-database lookup for these IDs.

The self-identity route is an extension to the pinned Hypershell reference.
The reference CLI shows token claims but does not return the stored user ID.
The extension has an [OpenAPI contract](../contracts/extensions/current-user.openapi.yaml).
The route returns the stored KSUID, reference fields, username, email, and name.
Extension version 1.1.0 also returns the verified token's issuer, subject, and
expiry as `issuer`, `subject`, and `expires_at`. These values describe the token
used for this request and are not new stored user properties. The subject is
distinct from the application KSUID. Strict clients must use the updated schema.
The generated [`whoami` command](cli-identity.md) uses these fields.
Its `href` points to the same self-identity route. The response has
`Cache-Control: no-store`.

The generated verifier must accept the token before the handler runs. The
verified issuer and subject select the user. Query parameters and request bodies
are rejected. The caller cannot select another user or request arbitrary roles through this
route. [Global role preparation](global-roles.md) now projects the two managed
roles from verified claims before this handler runs. No user directory or arbitrary user lookup is exposed.

On the first valid request, the application creates a user record. Later
requests update profile fields from the verified claims when those fields change.
The transaction reads the stored record before it commits. Creation and update
timestamps therefore describe the committed data. A failed write returns an
error, not the proposed profile. An unchanged profile does not cause a write.
Profile fields describe the claims in the current request; an older valid token
can still carry an older profile. Profile values never select an identity.

Concurrent first requests can return HTTP 409. Clients can retry the request.
The unique issuer-and-subject key prevents duplicate users. A deleted user also
causes a conflict; login does not restore that identity or its grants. An operator
must resolve that state through a trusted process. Changing a username, reusing
a profile name, or using the same subject under a different issuer cannot
transfer the stored identity.

| Check | Evidence |
| --- | --- |
| Response contract | `TestCurrentUserThroughGeneratedRuntime` checks real responses against the extension schema |
| Authentication | Missing, forged, expired, wrong-audience, and old-issuer tokens fail |
| Request limits | Target selectors and request bodies fail before they write an identity |
| Identity | Profile changes keep the ID; reused names and different issuers receive different IDs |
| Stored data | A failed profile update returns an error and preserves the previous profile |
| Concurrent registration | Direct and REST tests force eight first inserts to overlap; callers retry conflicts and receive one stored user ID without role grants |
| Restart | The same user ID and timestamps remain after the generated process restarts |
| Deleted user | Login cannot restore a deleted identity |
| Complete sharing inputs | Real browser login obtains the recipient and role IDs through REST before the owner grants access |

STEGO supplies verification, request handling, storage, and transactions. The
variant supplies the public response and the rule that only the caller's record
can be returned. The self-identity handler uses existing storage contracts. Global-role
preparation requires the migration in [its workflow notes](global-roles.md).

The focused race checks passed with PostgreSQL and Keycloak required in
34.467 seconds. The full local race suite also passed; the acceptance package
took 297.088 seconds. Dependency verification passed.

The broader enterprise goal remains open. A searchable user
directory is still a separate policy decision. This extension does not claim
complete user administration, immediate token revocation, or production capacity.

A local benchmark read the current user with 10,000 unrelated users. Across 100
calls, it averaged 0.356 ms, 26,644 bytes, and 354 allocations per call. It used
Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H. It includes the
transaction, identity lookup, and stored-row read. It excludes token verification,
HTTP, global-role preparation, first-time registration, profile writes, and
concurrent load. Run
`go test -mod=readonly -run '^$' -bench '^BenchmarkCurrentUserLookup$' -benchtime=100x ./acceptance`
with the PostgreSQL test settings. This is a local measurement, not a production
capacity claim.

On 2026-09-11, the concurrency checks ran in the jshell cluster. A test-only
database trigger holds each first insert until all eight transactions reach it.
The test then releases the inserts and requires at least one conflict. Each
caller retries the complete operation with a delay, a 10-second shared deadline,
and a maximum of 64 attempts. Direct calls retry only storage conflicts and
serialization errors. REST calls retry only HTTP 409. Other errors fail the test.
The production transaction callback still runs once per call. The runtime does
not replay application work.

Ten direct runs and ten REST runs passed under race detection in 46.210 seconds.
Each run observed seven conflict retries, one stored identity, and no role grant.
The current-user and generated SDK Gateway workflows also passed in 13.273
seconds. These checks prove the specified test case. They do not guarantee that
all conflicts will clear within a fixed retry count under arbitrary load.
