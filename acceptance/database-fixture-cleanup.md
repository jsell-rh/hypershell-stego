# Test database cleanup

Full run [35499921092](https://github.com/jsell-rh/hypershell-stego/actions/runs/35499921092)
failed in the test database cleanup after the late Keycloak creation test.
All 1,120 other core cases passed. The failure was at `gateway_test.go:67`,
where the fixture removed its private database with a five-second deadline.
The test output contains no application assertion failure for this case.

The retained PostgreSQL log shows a checkpoint starting at 08:52:33.508 UTC.
The database drop was canceled at 08:52:38.512 UTC. The checkpoint finished
at 08:52:43.431 UTC and reported 9.924 seconds. PostgreSQL 18.6
[waits for a checkpoint during database removal](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/commands/dbcommands.c).
This supports a checkpoint delay as the cause. The original run did not record
the backend wait event, so that cause remains an inference. The
[failure record](database-fixture-cleanup-failure-evidence.json) preserves
the source, log hash, failed case, and server observations.

Fixture cleanup now has a separate 20-second deadline. It makes one removal
request, then verifies database absence within the same deadline. A failure
remains a failed test. There is no cleanup retry that can turn it into a pass.
A separate diagnostic read has a two-second limit. Its output contains only
counts and fixed status fields; it excludes database names, connection strings,
SQL text, and error bodies. The focused provider test checks this query against
PostgreSQL and checks the fixed output when its connection pool is closed.

This change affects test fixtures only. It does not change application timeouts,
the production database provider, Gateway cleanup, or the 30-second target.
New focused and full checks must qualify this source. The failed run remains
a failure; the live test was not dispatched from that result.
