STEGO compiler `6663ed4fd717cb6c2aa78c05b2e4463d0cfbf4da` supplies the pool policy. The variant
uses `postgres-adapter` 3.15.0. The [compiler contract](https://github.com/jsell-rh/stego/blob/6663ed4fd717cb6c2aa78c05b2e4463d0cfbf4da/specs/database-pool-bounds.md)
defines the deployment settings and limits.

The Gateway pool-pressure gate tests STEGO's connection-pool policy through the
generated API. Pool settings belong to deployment configuration. Hypershell
supplies no connection-pool wrapper or scheduling code.

`TestGatewayDatabasePoolWaitCancellationAndRestart` creates a Gateway through
REST and checks its owner grant and delivered event. It then holds two Gateway
reads behind a PostgreSQL table lock. A third request has a short deadline.
The pool must stay within two connections, and the waiting request must retain
its deadline. The test counts actual PostgreSQL sessions and excludes the
separately owned LISTEN session. It sends at most three simultaneous requests.

After the lock is released, both held reads must succeed. A different caller
must still receive the opaque 404 response. The owner must read the same
Gateway after API restart. This checks a small connection budget through the
real application transaction and authorization paths.

`TestGatewayRejectsInvalidDatabasePoolSettings` requires invalid settings to
stop the generated process with exit code 1 and one safe `database.open` failure
record. The process must not start its API listeners or report the setting
value or database connection details.

On compiler `5c0dd1f`, the first probe failed because the generated pool opened
three connections when two were requested. The application test took 6.64
seconds. Its source archive SHA-256 was
`09bcf77006fc8fc086f3fe54c8753a9f6eacab38ee04c71561299e43d1ae7b28`.
That run did not reach the later recovery assertions. It is a failed baseline,
not evidence for the new pool implementation.

The default open limit is 16. The default idle limit is the smaller of four and
the open limit. Connections have a 30-minute lifetime and a five-minute idle
limit. Operators must budget all replicas and independent connections against
the server's capacity. Waiting request count, startup connection deadlines,
pool metrics, separate clients, and production capacity remain separate work.

Compiler CI [34605141391](https://github.com/jsell-rh/stego/actions/runs/34605141391)
passed for `6663ed4fd717cb6c2aa78c05b2e4463d0cfbf4da`. The application update
requires a separate full CI result.

With the pinned pool compiler, all 17 selected acceptance tests passed under
race detection in 104.514 seconds. The pressure, cancellation, and restart test
passed in 5.66 seconds. Invalid-setting startup rejection passed in 3.13 seconds.
The selection also covered CLI calls, atomic writes, rollback, filtered access,
REST/gRPC, event delivery, health recovery, telemetry, task failure, and replica
identity. Internal and contract race tests passed. Static analysis passed.
The application command returned exit code 0.

Two generation passes preserved all 102 output, state, and dependency hashes.
Downloaded files matched those hashes. All 209 application Go source files
matched the tested snapshot before commit. Its source archive SHA-256 was
`4b21eae2e6444dc27e9993b3d1aa1cca0f3461f5852bb6d98bdd35e2a7ee495a`.
The checks ran in a separate source directory in Job `pool-workflow`, namespace
`stego-pool-20260911`, with Go 1.26.8 and PostgreSQL 18.6. The test container had
one CPU, 3 GiB of memory, and `GOMAXPROCS=1`. PostgreSQL had half a CPU and
512 MiB. The Job had a 30-minute deadline and no mounted service-account token.
PostgreSQL listened on Pod loopback only. No performance test ran on the PC.
The failed baseline and successful compiler and application checks have separate
logs and result records. These checks do not replace full application CI.
