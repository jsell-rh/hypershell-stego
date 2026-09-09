The account discovery workflow runs through the generated HTTP process and
PostgreSQL. The reference CLI sends `sort` and `order` even for a default list.
The previous variant rejected those parameters with HTTP 400. The new workflow
starts with creation, finds accounts, revokes a selected account, repeats the
query after process restart, and deletes the account.

The list accepts `page`, `size`, `status`, `search`, `sort`, and `order`. Defaults
are page 1, size 20, and descending creation time. Page size is 1 through 100.
The page number has the existing 1,000,000 limit. Unknown or repeated parameters
are rejected. Empty page, size, sort, and order values select their defaults.

Search is a literal substring match against name, client ID, or subject. It is
case insensitive under the PostgreSQL database collation. Percent, underscore,
backslash, and the SQL escape character remain literal. Search accepts up to
4,096 bytes of valid UTF-8 without NUL. An empty search adds no text condition.
The status choices follow the reference list contract: provisioning, ready,
degraded, expired, revoking, revoked, deleting, and error. The reference handler
omits degraded, but OpenAPI permits it. The variant follows OpenAPI, and the
test checks each status choice from that contract.

Sort fields are name, role, status, expires_at, and created_at. Order is asc or
desc. Text order follows the database collation. The account ID supplies a
second sort key in the same direction. Both
count and paging follow the Gateway scope, current grant, creator restriction,
status, and search conditions. Owners see their Gateway's accounts. Viewers see
only their own accounts. Admin status alone does not grant account access.
Deleted accounts remain absent. Responses contain no client secret.

STEGO supplies the general `TextMatch` storage condition. Hypershell selects
its account fields and public options. No account field or status enters the
compiler. Generated tests use separate Lease and Record entities to check
literal matching, NULL values, scopes, paging, invalid fields, and bounds.

`TestServiceAccountDiscoveryThroughGeneratedRuntime` is in the required
acceptance suite. It covers all five sort fields, page boundaries, literal
search, filtered totals, denied requests, other Gateways, revocation, deletion,
and restart. It also checks malformed queries and secret exclusion.

The same test can run an external reference CLI against each valid query. Set
`STEGO_TEST_REFERENCE_CLI` to an absolute executable path. The test writes an
isolated mode-0600 configuration with a test token and sets a ten-second command
deadline. It checks the CLI results against the HTTP results. The local check
used the CLI from reference revision
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`. This extra compatibility probe is not
required by hosted CI. It does not constitute the complete STEGO client port.

One local benchmark used Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9
185H. It created 5,000 revoked account records, with 1,000 under the selected
Gateway. A status and literal search returned 100 matches in that Gateway.
Across 100 calls, the 20-row page took 7.19 ms per operation, 111,362 bytes,
and 1,875 allocations. This includes domain access checks, PostgreSQL count,
and paging. It excludes HTTP, provider calls, and concurrent load. Run
`go test -run '^$' -bench '^BenchmarkServiceAccountDiscovery$' -benchtime=100x ./acceptance`.
Substring matching can scan history. This measurement does not establish
production capacity, a latency percentile, or large-history retention policy.

The complete local variant race suite passed with PostgreSQL and Keycloak
required. Its acceptance package took 437.363 seconds. This run included the
reference CLI probe. The final status correction then passed the focused
workflow again. Pinned regeneration and static checks passed. Hosted CI repeats
the full suite against the final commit.
