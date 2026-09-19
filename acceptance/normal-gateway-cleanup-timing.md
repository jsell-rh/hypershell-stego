# Normal Gateway cleanup observations

The live browser workflow records normal final Gateway deletion separately from
its earlier SQL-denial recovery case. Each sample starts after the owner receives
HTTP 202. Before that request, a bounded SQL read records the total and undeleted
account row counts. An already-deleting Gateway cannot start a new sample.

A sample completes only after the existing checks find all four allocated
namespace profiles absent, account and identity cleanup complete, workload and
SQL cleanup finalized, allocation bindings absent, and Gateway and console SQL
roles and databases absent. The owner API must also return HTTP 404. The test
then checks that the supplied PostgreSQL server and installation data remain.

Completion checks are sequential. The elapsed value is an observed upper bound,
not an exact controller completion time. An upper bound at or below 30 seconds
proves that this sample was observed complete within the target. A larger upper
bound cannot prove when an earlier unobserved completion occurred. It requires
further measurement; it must not be reported as an exact latency failure.

The record is `gateway-cleanup-timing.json`. It includes the observation method,
phase, acceptance time, account counts, per-Gateway completion state, elapsed
upper bounds, and preservation result. Partial records survive a test failure.
A successful live workload run must supply the file; missing and empty files
fail evidence collection. The record contains no credentials or response bodies.

This is not the production capacity fixture. In particular, the current browser
workflow does not create 100 accounts for every remaining Gateway. Its record
sets `capacity_fixture` to false. A complete 100-account Gateway cleanup test is
still required, in addition to the separate account-only capacity measurement.
The 30-second target and the existing fault and late-effect checks are unchanged.

The local collection test uses fake files and a fake `oc` command. It passed with
the new missing-file and empty-file cases. Hosted adapter run `35475728036`
passed all 63 required tests and all 28 collection cases. The live test compiled.
The SQL integration test remains excluded from this adapter-only gate. See the
[adapter evidence](normal-gateway-cleanup-timing-evidence.json).
The live observation remains pending. No live timing result is claimed.
