STEGO supplies the database connection and startup ping deadlines. The variant
adds no production connection wrapper. PostgreSQL adapter version 3.16.0 gives
each connection operation five seconds. The generated process gives its startup
ping five seconds, including connection acquisition. GORM does not issue an
extra automatic ping. A process stop cancels the startup ping.

`TestGatewayDatabaseStartupDeadlineAndRecovery` creates a Gateway through REST,
checks its owner grant, and receives the resulting event. It stops the process
and then tries to restart against two controlled database endpoints. One accepts
a TCP connection and stops before authentication. The other accepts PostgreSQL
startup and stops at the first query. Each process must exit with code 1 and one
safe `database.ping` failure before the external test deadline. Neither may
start an API listener or expose credentials in its failure record.

The test also sends SIGTERM during a stalled ping and requires prompt exit.
It then restarts with the real database, reads the original Gateway as its
owner, and requires a denied response for an unrelated user. Companion checks
cover pool waits, caller cancellation, invalid pool settings, health recovery,
private diagnostic data, and the generated SDK Gateway workflow over REST and
gRPC. The SDK workflow also checks atomic writes, events, and filtered lists.

The initial probe ran against compiler `c06b951`. Gateway creation, the owner
grant, and event delivery passed. Restart then remained stuck before database
authentication. The external eight-second test context killed the process.
The package failed in 14.625 seconds and did not reach the later recovery checks.
The initial source archive SHA-256 was
`87b088bc7761128c0c39b8f91e4da32fea88eff0d8d2b853ef3edb29d7eb0abd`.

This gate does not establish database TLS policy or bounds for every startup
callback. It does not change transaction retries, migration policy, or the
separate event-listener connection. Those remain separate requirements.

Compiler `eed65783f6572813fd3c355203904ada7bac1abf` passed
[full CI run 34611767365](https://github.com/jsell-rh/stego/actions/runs/34611767365).
Two fresh builds of that commit produced identical hashes for all 106 generated,
state, and dependency files. The hashes also matched after tests and after the
files were copied to the local checkout.

Eight selected race tests passed against the pinned output in 50.906 seconds.
The startup deadline and recovery test took 15.48 seconds. The database telemetry
test passed in 5.55 seconds and retained trace correlation across restart.
Contract tests passed in 1.559 seconds on the preceding candidate output.
Acceptance and generated-code static checks and module verification passed.
No dependency version changed. Full application CI remains a separate gate.

The checks ran on 2026-09-11 in Job `db-startup`, namespace
`stego-db-startup-20260911`, with Go 1.26.8 and PostgreSQL 18.6. The test
container had one CPU and a 3 GiB memory limit. The database had half a CPU
and a 512 MiB memory limit. The job deadline was 30 minutes. No local build
or performance test was used. The pinned application archive SHA-256 was
`00195e2c9d5687bb3a3ddca4f5f715e3f87328043b7f95a0d00165e30d66d8d5`.
Final local edits changed evidence documents only.

The first application preparation attempt failed before tests. It copied the
registry default `internal/storage` over the variant's `storage` namespace.
Restoring the variant namespace while retaining version 3.16.0 corrected that
integration mistake. The later candidate and pinned checks use `storage`.
