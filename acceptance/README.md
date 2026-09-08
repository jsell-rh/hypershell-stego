The Gateway acceptance gate remains open.

| Check | Current evidence |
| --- | --- |
| Create and retrieve | Domain service, KSUID, namespace, placement, timestamps |
| Atomic creation | Gateway, owner grant, and event commit or roll back together |
| Access | Owner, viewer, admin, removed grants, opaque reads, filtered pages and totals |
| Event delivery | Generated process sends committed events to a TLS Kafka protocol fixture |
| Restart | New store retains the Gateway and grant; new event process drains pending events |
| Regeneration | Pinned compiler, apply, dependency check, repeated apply, and drift check |
| REST | Pending |
| gRPC | Pending |

Set `STEGO_TEST_POSTGRES_DSN` to a PostgreSQL connection with permission to create
test databases. Set `STEGO_REQUIRE_POSTGRES=1` to require these checks. Each test
creates and removes its own database. It does not migrate or delete the supplied
database. Run `go test -race -mod=readonly ./...`.

The tests apply generated model and outbox migrations during setup. The event
process does not apply migrations. It fails if the queue is absent. The broker
is the upstream franz-go protocol fixture with verified mutual TLS. It is not a
production Kafka deployment.

The current application service accepts a verified principal from a transport
adapter. Direct domain tests supply this principal. A separate signed-token test
uses the generated verifier and configured issuer roles to create a Gateway and
check access after creator-role removal. It also rejects an incorrect issuer
and a role from an unselected claim. This does not prove REST/gRPC endpoint
enforcement. Both transports must use the same domain service before the
acceptance gate can pass. Deployment database
placement, platform-role projections, complete user and role models, and all
other Hypershell workflows remain open.

One local benchmark used PostgreSQL 18.6, Go 1.26.8, and an Intel Core Ultra 9
185H. It read a 20-row page from 200 Gateways, of which 100 were visible to the
caller. Across 100 iterations, the domain call took 16.86 ms per operation,
89,080 bytes, and 1,349 allocations. This is a small-data baseline. It is not a
capacity or production latency claim. Run it with
`go test -run '^$' -bench BenchmarkGatewayFilteredPage -benchtime=100x ./acceptance`.
