The requested Gateway workflow gate passed. Local race tests, pinned
regeneration, and remote CI passed for
[the implementation](https://github.com/jsell-rh/hypershell-stego/actions/runs/34286086055)
and [its compiler](https://github.com/jsell-rh/stego/actions/runs/34286053277).
This is the first application workflow. The complete Hypershell and enterprise
readiness goals remain open.

Run `scripts/check-gateway.sh` for the complete gate. See
[the five requirements and their tests](gateway-workflow.md). CI uses the same
command. The generated application processes also use the race detector.

| Check | Current evidence |
| --- | --- |
| Create and retrieve | Domain service, KSUID, namespace, placement, timestamps |
| Atomic creation | Gateway, owner grant, and both creation events commit or roll back together |
| Access | Owner, viewer, admin, removed grants, opaque reads, filtered pages and totals |
| Event delivery | REST creation commits an event that the same generated process sends to a TLS Kafka protocol fixture |
| Restart | New store retains the Gateway and grant; new event process drains pending events |
| Regeneration | Pinned compiler, apply, dependency check, repeated apply, and drift check |
| REST | Create, get, patch, delete, search, ordering, filtered counts, response schema, error shape, viewer access, grant removal, rollback, watch streams, and restart |
| gRPC | Generated wire descriptors match the reference; TLS create/get/update/delete/list, count adjustment and set, access, rollback, events, cross-transport reads, watch streams, and restart |

Set `STEGO_TEST_POSTGRES_DSN` to a PostgreSQL connection with permission to create
test databases. Set `STEGO_REQUIRE_POSTGRES=1` to require these checks. Each test
creates and removes its own database. It does not migrate or delete the supplied
database. Run `go test -race -mod=readonly ./...`.

The tests apply generated model and outbox migrations during setup. The application
process does not apply migrations. It fails if the queue is absent. The broker
is the upstream franz-go protocol fixture with verified mutual TLS. It is not a
production Kafka deployment.

The current application service accepts a verified principal from a transport
adapter. Direct domain tests supply this principal. A separate signed-token test
uses the generated verifier and configured issuer roles to create a Gateway and
check access after creator-role removal. It also rejects an incorrect issuer
and a role from an unselected claim. The REST process test uses signed tokens
and the same domain service. It rejects reserved fields, duplicate JSON members,
case aliases, invalid Unicode, and NUL characters that PostgreSQL cannot store.
It also forces owner-grant and event-write failures and checks rollback.
The gRPC transport uses the same domain service. Its list defaults and page
size metadata follow the reference gRPC adapter. REST and gRPC requests can
retrieve each other's created resources. REST search and ordering tests include
`OR` expressions, literal injection attempts, invalid fields and value types, and
count-only requests. Related-resource search, sparse fields, REST page sizes above 100,
deployment database placement, platform-role projections, complete user and role models, and all
other Hypershell workflows remain open.

One local benchmark used PostgreSQL 18.6, Go 1.26.8, and an Intel Core Ultra 9
185H. It read a 20-row page from 200 Gateways, of which 100 were visible to the
caller. Across 100 iterations, the domain call took 16.86 ms per operation,
89,080 bytes, and 1,349 allocations. This is a small-data baseline. It is not a
capacity or production latency claim. Run it with
`go test -run '^$' -bench BenchmarkGatewayFilteredPage -benchtime=100x ./acceptance`.

The REST handler benchmark adds token verification, creator-name lookup, and
JSON output to a 20-row filtered page from the same 200-row data set. It measured
17.58 ms, 156,735 bytes, and 1,923 allocations per call across 100 iterations.
It uses an in-process HTTP recorder and excludes network latency. Creator-name
lookup uses one parameterized query for the whole page. Run
`go test -run '^$' -bench BenchmarkRESTFilteredPage -benchtime=100x ./acceptance`.

The gRPC benchmark reads a 20-row filtered page from the same 200-row data set
through a separate generated application process. It includes TLS, token
verification, PostgreSQL access, and protobuf messages. The connection is
established and pending events are delivered before measurement. On the same
local environment, 100 requests averaged 16.79 ms each. It does not measure
concurrent capacity, startup, TLS handshakes, or server memory. Run
`go test -run '^$' -bench BenchmarkGRPCFilteredPage -benchtime=100x ./acceptance`.

Mutation tests cover REST patches, gRPC updates, and deletion through both
transports. They check the pinned response schema, field presence, unchanged
placement, protected fields, owner and viewer access, admin restrictions, and
control-plane subjects. Event-write failures roll back updates and deletions.
A paused patch and a concurrent PostgreSQL write prove that a stale whole-row
update cannot overwrite a newer sandbox count. The failed patch returns a
serialization conflict and leaves no event.

The generated-process test changes resources across transports. It then stops
the process, commits an update and deletion, and restarts the process. The
runtime delivers both events in commit order, and both transports exclude the
deleted resource. Separate successful REST and gRPC deletion requests verify
HTTP 204 without a body and the protobuf delete response. PostgreSQL tombstones
supply authorized delete events to the watch implementation. Service-account cleanup
and production broker tests remain open.

A local gRPC patch benchmark performed 100 updates to one Gateway through a
separate generated process. It averaged 3.297 ms per request on the same local
Go 1.26.8, PostgreSQL 18.6, and Intel Core Ultra 9 185H environment. Each request
included token verification, an owner check, a serializable mutation, and an
outbox insert. The runtime worker delivered events in the background. The
measurement excludes startup and TLS connection setup. It does not establish
concurrent capacity, delivery latency, or server memory use. Run
`go test -run '^$' -bench '^BenchmarkGRPCGatewayPatch$' -benchtime=100x ./acceptance`.

Sandbox-count tests cover adjustment, absolute set, NULL-to-zero conversion,
zero flooring, unchanged values, and the signed 32-bit limit. Only configured
control-plane subjects can write a count. Missing and deleted namespaces return
zero without an event. An event-write failure rolls back the count. A changed
count updates the resource timestamp; an unchanged count does not.

The generated-process test issues 32 concurrent gRPC increments and a competing
Gateway patch. Every increment is retained. The test checks counts through REST
and gRPC, consumes all committed update events, and verifies recovery after a
count changes while the process is stopped. Reference protobuf descriptors
remain unchanged. The full local race suite passed with PostgreSQL required.

On Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H, a local benchmark
ran 100 increments against one Gateway. Sequential requests averaged 2.035 ms.
The concurrent run measured 2.226 ms per operation as a throughput measure,
not individual request latency. Both runs used a separate generated process,
TLS, signed tokens, row locks, and outbox writes. The worker delivered events
in the background. The measurements exclude startup and TLS connection setup.
They do not establish production capacity or end-to-end event latency. Run
`go test -run '^$' -bench '^BenchmarkGRPCSandboxCount$' -benchtime=100x ./acceptance`.

Gateway watch acceptance now runs through the generated application process.
Two owner streams receive the same events. Viewer, admin, and control-plane
streams follow the current access rules. Hidden events and revoked grants do
not expose data. A forged delete notice for a live Gateway is ignored. The
next visible event acts as a barrier in each denied-event check.

The test subscribes before the initial list. It creates through REST, updates
through gRPC and REST, changes the sandbox count, and deletes through both
transports. Event fields match the gRPC response. An independent process writes
to the same database and its events also reach the streams. An event-write
failure rolls back the change. Kafka delivery remains active throughout.

Restart closes old streams. After an offline change, a new subscription and
list recover current state, and later events arrive normally. A separate test
terminates the dedicated PostgreSQL listener connection. The generated supervisor
closes both network listeners and exits with an error. Restart and a new list
recover the changed state. Signed-token tests check unauthenticated requests,
actual token expiry, and the configured stream lifetime.

These checks use PostgreSQL 18.6 and a TLS Kafka protocol fixture. They do not
prove broker failover, production watch capacity, or a complete control-plane
client. The STEGO tests separately check subscription overflow, process and
subject limits, unary availability, slow-peer I/O, and shutdown.

A local watch benchmark ran 100 sequential updates to one Gateway. It measured
4.773 ms per operation from the start of the gRPC update through receipt of its
watch event. It includes token verification, the database transaction, outbox
insert, live notice, access check, and response delivery through a separate
process with TLS. Kafka delivery ran in the background. The environment used
Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H. The measurement excludes
startup and TLS connection setup. It does not establish concurrent capacity,
latency percentiles, or server memory use. Run
`go test -run '^$' -bench '^BenchmarkGRPCGatewayWatch$' -benchtime=100x ./acceptance`.

The [service-account workflow](service-accounts.md) now covers a separate domain
through the generated REST process, TLS unary RPC client, storage, and recovery
task. It includes one-time secrets, role limits, pending revoke and delete,
audit failures, creator-grant changes, expiration, and restart. The
[Keycloak test](keycloak.md) also proves actual token issuance, role reduction,
drift repair, and revocation after restart. Production limits remain open.

The [Gateway identity workflow](gateway-identity.md) tests the generated stream
client with a real controller and Keycloak. It covers initial state, live events,
trusted provider bindings, token use, API restart, and offline deletion recovery.

Gateway grant changes now have an application workflow. Owners create and remove
grants through REST. The test checks access through REST and gRPC, filtered lists,
denied requests, event delivery, removal of access, re-grant, and restart.
A separate concurrent test protects the last owner. See
[the grant workflow and its remaining scope](gateway-grants.md).

The user login workflow now uses real Keycloak browser forms and PKCE S256.
Gateway tokens reflect owner and viewer grants, profile changes, removal, and
restart. The test exposed a missing subject mapper in the managed Gateway client.
See [the user login evidence and remaining scope](gateway-user-login.md).

The application now supplies filtered REST grant lists and the reference gRPC
grant list and watch service. See [the grant discovery evidence](grant-discovery.md)
for access rules, restart behavior, and limits.
