# Generated recovery storage cursors

Gateway ID recovery, managed-database deletion replay, and service-account
recovery now use STEGO's CursorReader. PostgreSQL adapter 3.10.0 builds the bound
ID predicate, database ordering, filters, row limit, and continuation result.
These application paths no longer build an ID search expression or request an
unused total. The normal REST and gRPC list totals remain unchanged.

Hypershell still checks canonical IDs and recovery permissions before storage
access. Gateway recovery includes live and retained deleted IDs. Database replay
selects only deleted databases. Service-account recovery supplies its status and
time rules, with separate live and deleted streams. The deleted streams now
exclude live rows at the database query. Every action still checks current state
before provider work.

The reader returns at most the requested number of rows. One extra row sets the
More flag without a count query. Database replay and service-account recovery use
that flag. The private Gateway ID protocol keeps its existing ID-only shape and
page termination rule. The application does not compare ID order with Go string
order; the database collation owns ordering.

The generated query shares ordinary list filters and current-observation
projection. Selecting deleted roots does not make deleted related grants live.
Each query has a ten-second maximum or the caller's shorter deadline. The
[compiler contract](https://github.com/jsell-rh/stego/blob/main/specs/storage-cursors.md)
states the input and result limits, transaction rules, and benchmark results.

The new TestRecoveryPagesAvoidTotals first failed against the old implementation
in 0.261 seconds. Each Gateway or database recovery page ran one count and two
total reads. With the generated cursor, it passed in 0.44 seconds: each page
uses one read and no count. Denied callers perform no read. The query recorder
stores only counters and does not log SQL values or resource contents.

The focused database replay, recovery-query, and partial service-account recovery
checks passed in 28.041 seconds. Database replay took 12.61 seconds and covered
C and ICU ordering, multiple pages, denied calls, capability headers, complete
retained deletion records, and API restart through TLS gRPC. Partial account
recovery took 13.95 seconds. The separate Gateway ID recovery check passed in
4.10 seconds and covered 205 IDs, denied requests, and restart. Durations include
test setup.

The real database Kubernetes gate passed in 66.974 seconds. It checked TLS,
persistence, foreign namespace denial, offline deletion, late effects, and
deletion replay. The complete Gateway Kubernetes gate passed in 206.104 seconds.
It checked database and OIDC setup, access denial, three service-account
identities, Pod and database restart, namespace replacement, stable keys, and
offline cleanup across cluster targets. These runs used the pinned compiler
18337ac98127a32d96b5bafb05d6b8d2a58693dd.

The full PostgreSQL/Keycloak race suite passed on 2026-09-10. Its acceptance
package took 673.043 seconds. Static checks and module verification passed.
The workload gates and full suite used separate test databases on the same host;
their durations are test evidence, not production performance targets.

This change does not make recovery a durable snapshot. Concurrent writes can
change later pages. Watches, repeated scans, and current-state checks remain
necessary. Durable retries, cross-process fencing, identity cursor memory bounds,
other count-based discovery paths, index coverage, and production capacity remain
open. There is no general production latency claim.

[Database recovery](database-recovery.md) now uses the same generated cursor for
both live and deleted rows. The controller no longer uses public list offsets or
unused totals to find live databases. The old deleted-only replay remains valid.
