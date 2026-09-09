Gateway owners can now create, read, and remove Gateway grants through the REST
API. A viewer can read the Gateway and the viewer's own grant. A viewer cannot
change the Gateway or its grants. A platform-admin token alone does not permit
grant changes. A configured control-plane subject can manage Gateway grants.

The application accepts only Gateway owner and viewer roles for this workflow.
The target user must already have a stored, verified issuer and subject. Profile
names do not select the identity. Global role assignment remains outside this
API workflow.

Each grant change locks its Gateway row. Creation writes the grant and two events
in one transaction. Removal checks the number of live owner grants under that
same lock. It refuses to remove the last owner. Concurrent owner removals cannot
both succeed. An event-write failure rolls back the grant change.

STEGO supplies `unique_when_live` for this requirement. A deleted grant keeps its
ID and data. A later grant has a new ID and new event IDs. The unique index starts
with the Gateway and role fields to support the owner count. The compiler's
separate Lease, Reservation, and Alias tests cover deletion, new creation,
concurrent duplicates, normal unique keys, and NULL values.

| Behavior | Test |
| --- | --- |
| REST grant creation, reads, removal, and re-grant | `TestGatewayGrantWorkflowAcrossTransportsAndRestart` |
| Gateway access and filtered lists through REST and gRPC | The same transport test |
| Grant event delivery through the generated runtime | The same transport test checks Kafka payloads, keys, kinds, and message IDs |
| Watch access after removal | A later visible Gateway event acts as a barrier for the removed Gateway update |
| Restart with pending grant events | The transport test commits a new grant while delivery is stopped, then checks the new process |
| Last-owner protection under concurrent requests | `TestConcurrentGrantRemovalPreservesLastOwner` |
| Grant and event rollback | `TestGrantEventsCommitWithGrantChanges` |
| Invalid targets and unbound profiles | `TestGrantRejectsInvalidTargets` |
| Index migration and retained deleted rows | `TestLiveGrantMigrationPreservesDeletedRows` |

For an existing variant database, apply earlier migrations first. Stop the old
application and apply
`migrations/000003_live_gateway_grants.sql` before starting the new version.
The migration creates the partial index and removes the previous full index
in one transaction. It has lock and statement timeouts. The migration test
recreates the previous index, reproduces the blocked re-grant, applies the
migration twice, and checks retained history and live uniqueness. Startup
migration alone cannot remove the old full index. No reference or production
database was changed.

The transport tests use signed test tokens, private PostgreSQL databases,
and a Kafka protocol fixture with mutual TLS. The fixture reads opaque user
and role IDs from the database. It does not insert the tested grants. Grant
changes use REST or the domain service while the process is stopped.

RoleBinding list and watch APIs, full Users and Roles APIs, global role
synchronization and device login remain open. Browser login now has
[a separate application check](gateway-user-login.md). Gateway
watch remains a live stream. Clients must list current resources after they
connect again. These checks do not establish production capacity or broker
failover behavior.

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package took 252.023 seconds. Dependency verification and pinned
regeneration passed.

A local benchmark performed 100 owner-grant creation and removal cycles with
10,000 unrelated owner grants. One cycle averaged 5.861 ms, 252,203 bytes, and
3,455 allocations. It used Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9
185H. The measurement includes access checks, the last-owner count, transactions,
and event writes. It excludes transport, event delivery, and concurrent load.
Run `go test -run '^$' -bench '^BenchmarkGatewayOwnerGrantCycle$' -benchtime=100x ./acceptance`
with the PostgreSQL test settings.
