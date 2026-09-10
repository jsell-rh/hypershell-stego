The application now provides REST grant lists and the reference gRPC
`RoleBindingService`. Clients can discover current grants and receive changes
through the generated runtime.

A caller can read its own grants and the grants on Gateways that it owns.
Configured control-plane subjects can read all Gateway grants. A platform-admin
role alone does not grant access to this inventory. This follows the variant's
existing grant-read rule. The reference server has coarser role checks; the
variant does not copy that broader disclosure. The
[global role contract](global-roles.md) covers global grant projection.

The database applies access, live-Gateway requirements, search, and ordering
before it counts or pages rows. REST supports page, size, search, and orderBy.
Its default and maximum page size are 100. Size zero returns only a count.
Unknown fields, duplicate query parameters, and invalid values fail.
[Field selection](field-selection.md) is supported. Larger REST pages remain open.

The gRPC list requires a user ID and permits a Gateway ID filter. It returns
role names and current profile names with the reference wire shape. Profile
names are display data; provider identity still uses the stored issuer and
subject through the private identity API. A missing role fails the whole
response. It never becomes an empty role in an otherwise successful response.
Role and user details are read in batches in the same database transaction.

The unpaged list and initial watch replay use a complete database snapshot,
with a limit of 10,000 visible grants. A larger result returns an error, not a
partial result. The gRPC list also rejects responses above 3 MiB. These are
resource limits, not production capacity claims. A transaction timeout can
stop a large response before either limit is reached.

The watch subscribes before it reads current state. It replays active grants
as update events, as required by the reference service. It then sends committed
create and delete events. It checks current access and stored deletion state
before each send, including replay. A former grantee can receive its own deleted
grant. It cannot read changes to other users' grants without current ownership.
A false deletion notice cannot present a live grant as deleted.

Gateway creation previously emitted only its Gateway event. The first runtime
watch test exposed the missing owner-grant event. Creation now commits the
Gateway, owner grant, and both events together. Failure of either event write
rolls back all of them. The generated runtime delivers both through the outbox.

Watch events are current-state notices, not an ordered history of all mutations.
Replay and concurrent events can overlap. Clients must handle duplicate notices.
A disconnected client must reconnect and recompute current state. Active replay
does not supply missed deletion history. The identity controller retains its
separate inventory of deleted grants for that recovery requirement.

STEGO supplies a new bounded `RowFilter` and joins through declared references.
The compiler contains no Hypershell role names or access policy. The application
constructs the union of grantee and owner access. Independent Record and
Membership tests check the same query features without Hypershell entities.

The acceptance checks are:

- `TestGrantDiscoveryThroughGeneratedRuntime`: REST shapes, paging, search,
  counts, gRPC filters, authentication, watch replay, current access, rollback,
  owner-grant events, outbox delivery, and restart.
- `TestGrantDiscoveryUsesCurrentRolesAndLiveGateways`: false deletion notices,
  unresolved roles, and grants whose Gateway is deleted.
- `TestGrantDiscoveryUnpagedResponseIsCompleteOrFails`: multiple database pages,
  complete role details, an excessive result, REST paging at the tail, and
  current ownership.
- `TestGrantDiscoveryRejectsOversizedGRPCResponse`: a large wire response fails
  with a resource-limit error, and a smaller filtered request succeeds.
- `TestGeneratedGrantDescriptorMatchesReference`: the generated protobuf
  descriptor matches the pinned reference, except for its Go import path.
- `TestEventFailureRollsBackGatewayAndOwner`: either creation event can fail
  without leaving a partial Gateway or grant.

User administration, remaining role query options, device login, workload
deployment, immediate token revocation, production capacity, and the remaining
enterprise requirements are still part of the active goal.

The final focused race checks passed in 13.703 seconds. The separate gRPC byte
limit check passed in 5.655 seconds. Dependency verification and pinned
regeneration also passed.

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package took 286.550 seconds. The later descriptor, row-boundary,
and byte-limit checks passed in the focused runs listed above.

A separate local benchmark requested a 20-row page with two visible grants and
10,000 unrelated grants. Across 100 calls, it averaged 4.271 ms, 92,691 bytes,
and 1,129 allocations per call. It used Go 1.26.8, PostgreSQL 18.6, and an Intel
Core Ultra 9 185H. This includes access checks, filtered count, database reads,
and role and user details. It excludes transport, concurrent load, and watch
replay. It does not establish production capacity. Run
`go test -run '^$' -bench '^BenchmarkGrantDiscoveryFilteredPage$' -benchtime=100x ./acceptance`
with the PostgreSQL test settings.

Hosted CI then exposed a timeout at the 10,000-grant boundary. The initial
snapshot repeated the root count and page scan 100 times. The fixture also
loaded users in bulk without updating planner statistics. PostgreSQL estimated
three live users where the table held 10,001. It chose the deletion index and
repeatedly scanned the user set during detail lookups.

The snapshot now counts and reads its bounded grant set once. It caches role
and user details within the transaction, reads each distinct ID once, and selects
only the ID and display field. Each detail lookup remains limited to 100 IDs.
The bulk fixture now runs `ANALYZE` before response-limit checks. The application
does not run `ANALYZE` during requests. The transaction deadline is unchanged.

A local diagnostic run measured user-detail reads at 3.94 seconds before
`ANALYZE` and 0.17 seconds after it. The query plan then used the primary-key
index with the correct row estimate. These measurements explain the fixture
correction; they do not establish a production latency bound.

With unchanged stale statistics, the code change reduced one local snapshot
sample from 6.48 seconds to 4.08 seconds. With current statistics, ten final
10,000-grant snapshots averaged 316.48 ms, 35,755,411 bytes, and 377,492 allocations
per call. The run used `GOMAXPROCS=2` and the race detector on the same local
environment. Run `go test -race -run '^$' -bench '^BenchmarkGrantDiscoverySnapshot$' -benchtime=10x ./acceptance`
with those processor and PostgreSQL settings. This excludes transport and
concurrent load.

The final focused race checks passed with `GOMAXPROCS=2` in 10.301 seconds.
They include complete and excessive snapshots, the gRPC byte limit, current
access, REST and protobuf shapes, event delivery, and restart. The small-page
benchmark on the final code averaged 4.095 ms, 91,536 bytes, and 1,091 allocations
per call across 100 calls without the race detector.

[Global role synchronization](global-roles.md) extends this inventory. Callers
can now read their own global records. These records omit the Gateway ID.
Existing Gateway access rules still apply. Watch preparation runs once per
stream; later events do not project the stream's old role claims again.
