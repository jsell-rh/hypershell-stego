STEGO compiler `5c0dd1fec8abdddfda7cdb7e15830a492c23560d` supplies shared
PostgreSQL driver telemetry. The variant uses `postgres-adapter` 3.14.1 and
`otel-tracing` 1.10.0. Generated main uses the adapter's pool factory and owns
pool closure. Hypershell adds no database tracing wrapper or provider.

The driver's operation context supplies the runtime and parent trace. Generated
storage emits fixed database call names, outcomes, CLIENT spans, local and OTLP
completion records, duration histograms, and an active-call count. SQL text,
arguments, connection details, credentials, and raw driver errors are excluded.
The [compiler contract](https://github.com/jsell-rh/stego/blob/5c0dd1fec8abdddfda7cdb7e15830a492c23560d/specs/database-observability.md)
defines callback coverage, limits, and separate performance evidence.

`TestGatewayDatabaseSignalsAcrossRestart` starts the generated API with real
PostgreSQL, a TLS broker fixture, and a TLS OTLP collector. It creates a Gateway
through REST, then checks its exact owner grant and delivered event. An event
constraint forces a second create to fail. The test requires no extra Gateway,
owner grant, or pending event after rollback. A different caller gets an opaque
404 on read and an empty filtered list.

After restart, REST and TLS gRPC must return the original Gateway to its owner.
gRPC must deny the other caller. Each request must produce a database span under
its server span, a correlated local and exported log, and a duration measurement.
The forced event failure must produce an error span. All signals must use the
current runtime identity; restart must create a new identity. Tokens, connection
settings, Gateway IDs, private names, and the private constraint name must not
appear in telemetry.

The probe on the former compiler `8ad255a` passed the first workflow assertions
but failed on missing database signals. On compiler `9cabca7`, the new test
passed in 5.14 seconds. The larger selected suite failed in two older tests that
assumed request and lifecycle signals were the only records. Request tests now
select their own scope and retain private-data checks across all received
signals. Database fields and correlation have a separate acceptance contract.
These failed attempts remain separate from results for the final compiler pin.

Startup migrations, contexts without a runtime, the separate event-listener
connection, external PostgreSQL clients, pool wait time, pool limits and
statistics, complete process logging, and production capacity remain open.
The broker is a protocol fixture; this test is not a production Kafka test.

Compiler CI [34603817927](https://github.com/jsell-rh/stego/actions/runs/34603817927)
passed for `5c0dd1fec8abdddfda7cdb7e15830a492c23560d`. Application CI requires
its own result for this adoption.

With the final compiler pin, 15 selected acceptance tests passed under race
detection in 89.978 seconds. The new database workflow passed in 5.31 seconds.
The selection also covered CLI calls, collector loss, runtime identities,
service logs, recovery sweeps, task failure, and restart. Internal and contract
race tests passed. Static analysis passed. This run returned exit code 0.
Two generation passes preserved all 102 output, state, and dependency hashes.
The downloaded output matched the same hashes.

The run used a separate source directory in Job `database-compiler-v2`, in the
`stego-database-20260911` namespace. Its source archive SHA-256 was
`73ef4576be94fba914b9fa39986771f06613ace220dddb2df6228bc8cb6cdee8`.
It used Go 1.26.8 and PostgreSQL 18.6. The test container had one CPU, 3 GiB of
memory, and `GOMAXPROCS=1`. PostgreSQL had half a CPU and 512 MiB. The Job had a
30-minute deadline and no mounted service-account token. PostgreSQL listened on
Pod loopback only. No performance test ran on the workstation. The Job's first
compiler snapshot failed; the later compiler and application commands each have
separate exit records. The later passes do not change that initial Job result.

Four additional race checks passed in 32.792 seconds: controller failure and
restart, gRPC watch tracing and collector loss, HTTP diagnostic privacy, and
HTTP tracing with restart and collector loss. Static analysis passed. This
command used a copy of the completed application snapshot with only the two
transport trace-test corrections. The correction archive SHA-256 was
`4a02b3394a4b3f3dee0a8c0af439c2c45a8163bba30554ae5a935d4ca1ec00d2`.
The command returned exit code 0. All 208 local application Go source files
matched those test snapshots before commit. Full application CI and provider
workflow results remain separate from these focused cluster checks.
