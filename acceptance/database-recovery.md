# Database recovery through generated cursors

The database controller recovers live and retained deleted IDs through one
finite stream. It uses STEGO's generated `ScanStream` client and `Scan` server.
The server reads each page through the generated storage `CursorReader`.
Hypershell defines recovery access, database IDs, and the event shape.

Recovery no longer uses public list offsets or list totals. Each page has at
most 100 rows and one lookahead row. The last returned ID selects the next page
in database order. Removal of an earlier live row cannot shift later IDs out of
that scan. Public REST and gRPC lists retain their existing totals and shapes.

The private gRPC request header is
`hypershell-managed-database-replay: retained-v1`. The response must confirm
that same header and `hypershell-managed-database-delete-tombstones: v1`.
Live rows use UPDATED events; retained deleted rows use DELETED events. Both
are hints. Each action reads current retained state before any provider call.
A replay event does not authorize deletion or supply authoritative provider data.

Only configured controller subjects can use replay. Ordinary users, Gateway
creators, and platform administrators are denied. The server sends capability
headers only after an authorized read. Unknown or repeated request modes are
invalid. The client rejects missing, repeated, or different response scopes.

The server still supports `deleted-v1` for existing clients. Deploy the API
before the new controller. If the server rejects retained replay as unsupported,
the new controller stops with a contract error. It does not silently accept an
incomplete replay. Temporary stream failures retain their status for retry.

The watch opens before recovery begins. Setup and each receive call have a
20-second limit. Generated queue admission applies backpressure and honors
cancellation. A scan accepts at most 1,000,000 rows; there is no total scan time
limit. Reconnect starts a fresh scan, and periodic scans repair missed events.
Each page has its own database snapshot. New IDs behind a cursor still require
watch delivery or another scan. This is not a durable snapshot or retry queue.

The retained replay test first failed in 7.691 seconds because the old API did
not support its mode. With this change, the focused checks passed in 58.136
seconds. They cover both replay modes, multiple pages, C and ICU database order,
provider and namespace fields, denied access, empty replay, API restart, an idle
stream, and cleanup through current retained state. The retained replay test
took 13.14 seconds, including setup and both collations.

A separate check deleted an earlier row between cursor pages and still recovered
all 21 IDs exactly once. The query recorder confirmed one read and no count per
retained page. Denied requests performed no read. Controller unit tests reject
invalid replay rows and response scopes, accept empty replay, preserve initial
stream errors, and require live and deleted IDs without an offset-list call.

STEGO already supplies the required query, scan, queue, and deadline mechanisms.
This change needs no new compiler API or dependency. The compiler pin remains
`46b5f4e5327dfd056cafb65fe3a39ff0cde74500`. Durable retry storage, complete queue
saturation handling, cross-process fencing, and production capacity remain open.

The real Kubernetes database gate passed in 94.465 seconds. It covered workload
provisioning, TLS, persistence, foreign namespace denial, offline deletion, and
both replay modes under C and ICU ordering. The complete Gateway Kubernetes gate
passed in 238.447 seconds. It covered database and identity setup, access rules,
service accounts, restart, namespace replacement, offline deletion, and cleanup
on a former cluster. These are local test durations, not production targets.

The complete PostgreSQL and Keycloak race suite passed on 2026-09-10 with 114
acceptance tests and a 941.540-second acceptance package run. Controller unit
tests, static checks, and module verification passed. The checks used Go 1.26.8
on Linux amd64, PostgreSQL 18.6, and the repository's pinned provider fixtures.
The Kubernetes gates overlapped part of the full suite. No production latency
or capacity claim follows from these elapsed times.
