# Persistent user identity

The API identifies a user by the verified issuer and subject pair. Username,
email, and name are profile fields. A profile change does not transfer or remove
a user's Gateway grants. Different subjects can have the same username in stored
history. A subject from a different issuer has a separate user record.

`TestGatewayGrantsFollowSubjectInsteadOfUsername` first reproduced an access
defect. A second signed subject with the owner's username could read, list, and
change the owner's Gateway. A username change also removed access from the
original subject. The test now checks denial through REST and gRPC, service-account
access, profile changes, restart, and an issuer change. The second subject cannot
reach the credential provider. Existing ownership survives a username change.

STEGO exposes the issuer only after signature and claim validation. Hypershell
stores that issuer with the subject and uses both fields for lookup. It also
checks the returned strings exactly before it uses a stored user. The composite
unique index prevents two records for the same identity. This follows
[OpenID Connect claim stability](https://openid.net/specs/openid-connect-core-1_0.html#ClaimStability).

## Existing databases

New databases receive the identity columns from the generated schema. Existing
variant databases require `migrations/000002_user_identity.sql`. Stop the old API
and recovery processes before the migration. The old code identifies users by
username and must not run against the new identity model. Apply the migration
through an administrator connection, then start the new application with new
database connections. Startup does not apply this migration.

The migration preserves all user IDs, grants, and account records. It adds
nullable issuer and subject columns and a composite unique index. It removes the
username uniqueness index. Both identity fields remain null for legacy users.
No username, email, or current login can adopt a legacy record or its grants.

A legacy record requires an explicit, reviewed mapping from its stored user ID
to a verified issuer and subject. Complete that mapping before application
restart if the existing access must remain active. The mapping must come from a
trusted identity source. The repository does not yet automate this migration.
It cannot infer the binding from the old database alone.

Without a trusted binding, recovery revokes the user's service-account clients
and retains their metadata and audits. Provider failure can delay that action.
Already issued tokens retain their expiry. `TestIdentityMigrationDoesNotAdoptLegacyGrants`
checks the previous User schema, repeated migration, new database connections,
refused adoption, and removal of the provider credential.

This change fixes API identity persistence. Gateway user-role synchronization
and completed browser or device login remain separate application work.

## Validation and lookup measurement

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package completed in 247.458 seconds. A separate lookup benchmark
used one owner, one Gateway, and 10,000 other users with the same display name.
Across 100 requests it measured 0.935 ms, 74,600 bytes, and 851 allocations per
Gateway read. This includes the domain access check and local PostgreSQL calls.
It excludes REST, gRPC, TLS, concurrent load, and provider operations. It is not
a production capacity or latency-percentile result.

The environment was Linux on an Intel Core Ultra 9 185H, Go 1.26.8, and PostgreSQL
18.6. Run the measurement with:

```sh
go test ./acceptance -run '^$' -bench '^BenchmarkGatewaySubjectLookup$' -benchtime=100x -count=1
```
