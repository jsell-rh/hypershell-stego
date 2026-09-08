The requested Gateway workflow gate passed. Local race tests, pinned
regeneration, and remote CI passed for
[the implementation](https://github.com/jsell-rh/hypershell-stego/actions/runs/34286086055)
and [its compiler](https://github.com/jsell-rh/stego/actions/runs/34286053277).
This is the first application workflow. The complete Hypershell and enterprise
readiness goals remain open.

| Check | Current evidence |
| --- | --- |
| Create and retrieve | Domain service, KSUID, namespace, placement, timestamps |
| Atomic creation | Gateway, owner grant, and event commit or roll back together |
| Access | Owner, viewer, admin, removed grants, opaque reads, filtered pages and totals |
| Event delivery | REST creation commits an event that the same generated process sends to a TLS Kafka protocol fixture |
| Restart | New store retains the Gateway and grant; new event process drains pending events |
| Regeneration | Pinned compiler, apply, dependency check, repeated apply, and drift check |
| REST | Create, get, filtered list, response schema, error shape, viewer access, grant removal, rollback, and restart |
| gRPC | Generated wire descriptors match the reference; TLS create/get/list, access, rollback, events, cross-transport reads, and restart |

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
retrieve each other's created resources. Full REST list search, custom ordering,
sparse fields, REST page sizes above 100,
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
