# Identity controller query cost

Before each provider role update, the identity controller reads the user's
verified identity and current Gateway grants through generated gRPC. The domain
service makes these reads in one storage transaction. Only configured
control-plane subjects can request this state.

The state read now uses STEGO's generated CursorReader for user, role, and grant
lookups. Each lookup requests at most one row. User reads include retained
deleted users; role and grant reads use live rows only. A deleted user retains
its issuer and subject for provider cleanup. A missing or unbound user returns
an error. A missing role definition also returns an error. Neither error can
supply a successful empty-role response.

The application still chooses owner before viewer and requires a grant for the
exact Gateway, user, role, and Gateway scope. The shared role lookup rejects a
second matching role or an inexact role name. STEGO constructs the queries,
binds values, limits rows, and validates cursor results. No application SQL or
new controller framework was added.

The initial query test failed in 0.469 seconds. It measured the following total
SELECT queries, including counts:

| Current state | Before | After |
| --- | --- | --- |
| Owner | 6, including 3 counts | 4, no counts |
| Viewer | 9, including 5 counts | 6, no counts |
| No current role | 9, including 5 counts | 6, no counts |
| Deleted user | 3, including 1 count | 2, no counts |
| Denied caller | 0 | 0 |

`TestIdentityUserStateUsesBoundedReadsWithoutTotals` checks these bounds and the
resulting role. It covers removed grants, a grant for another Gateway, deleted
users, unbound users, absent users, and denied callers. The existing current-grant
test checks role union and retained cleanup targets.
`TestIdentityUserStateRejectsMissingRoleDefinition` checks that incomplete role
metadata cannot cause provider cleanup. The focused race run also includes the
concurrent Gateway update regression. All four tests passed in 2.292 seconds.

The role lookup is shared with Gateway creation, access checks, and global role
synchronization. The storage adapter must supply CursorReader. The concurrent
update test wrapper forwards this capability to the real transaction and keeps
its write pause.

Three paired local benchmark samples used 100 owner-state reads each, with
10,000 unrelated users and grants and current database statistics. Before the
change, each call took 0.979–1.121 ms, allocated 50,094–50,130 bytes, and made
663 allocations. After the change, each call took 0.817–0.855 ms, allocated
55,136–55,493 bytes, and made 680–681 allocations. The host used Go 1.26.8,
PostgreSQL 18.6, Linux amd64, and an Intel Core Ultra 9 185H.

The result has fewer database queries and lower local elapsed time, with higher
allocation cost. The benchmark excludes gRPC, Keycloak, controller scans, and
concurrent load. It does not establish production capacity. Run it with:

```sh
STEGO_REQUIRE_POSTGRES=1 go test -count=3 -mod=readonly -run '^$' \
  -bench '^BenchmarkGatewayIdentityUserState$' -benchmem -benchtime=100x ./acceptance
```

Set STEGO_TEST_POSTGRES_DSN to the test database connection. The fixture creates
and removes its own database.

The identity inventory still uses numbered pages and process-local progress.
This change does not make that progress durable or prove its memory bound.
Multiple-controller fencing and immediate revocation of issued tokens also
remain open. See [Gateway user login](gateway-user-login.md) and
[Gateway scheduling](gateway-scheduling.md).

The full query-change race suite passed with PostgreSQL and Keycloak required.
It passed 116 top-level acceptance tests in 855.347 seconds. The three Kubernetes
tests were skipped in that run because they require the separate cluster gate.
Static checks and module verification passed. The first cluster gate found a
separate generated gRPC client defect. That defect was corrected in STEGO, and
the complete Gateway cluster gate then passed. The query behavior required no
further source change.
