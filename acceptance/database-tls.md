STEGO supplies the database transport policy. PostgreSQL adapter version 4.0.0
requires verified TLS by default. Configure `DATABASE_URL` with
`sslmode=verify-full`. Supply `sslrootcert` for a private certificate authority.
The certificate must match the database host name or IP address. The generated
event listener retains the pool's validated TLS configuration.

On 2026-09-11, the user approved verified TLS by default with the explicit
loopback test exception below. The generated behavior matches that decision.

`TestGatewayDatabaseTLSAndListenerAcrossRestart` uses native PostgreSQL TLS with
a certificate for `localhost`. It creates a Gateway through REST, checks the
owner grant, and receives the event. It checks `pg_stat_ssl` for the application
pool and the LISTEN session. Both must use TLS. Owner access must succeed through REST and gRPC. An
unrelated user must receive a denied response and an empty filtered REST list.
These checks repeat after restart.

The test rejects plaintext, unverified TLS, missing hostname checks, and fallback
to plaintext. It also rejects a wrong certificate name and a wrong trust root.
Each rejected startup must exit promptly with one safe failure record. The
record must exclude database credentials, database names, and certificate paths.

Set `STEGO_TEST_POSTGRES_CA_FILE` to the fixture's public CA file. The fixture
certificate must include `localhost` and exclude `127.0.0.1` from its names.
The full acceptance script requires this fixture. CI configures its temporary
PostgreSQL service with `scripts/enable-test-postgres-tls.sh`. The script creates
a temporary certificate, enables TLS, and verifies an actual TLS session before
it starts the tests. This script supplies test infrastructure only.

Other bounded fixtures use literal loopback addresses and the explicit setting
`STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK=1`. This setting permits plaintext only
to literal loopback IP addresses. The TLS acceptance test sets it to `0`, which
also matches the production default when the setting is absent. DNS names,
remote IP addresses, Unix sockets, and unverified TLS cannot use the exception.

This test does not establish certificate rotation or production capacity. The
existing Gateway SDK gate supplies companion REST, gRPC, atomic-write, filtered
list, event, and restart checks. Full application CI and fresh regeneration
remain separate checks. The wider enterprise goal remains open.

The first probe used compiler `eed6578`. Gateway creation, the owner grant,
event delivery, TLS session checks, and REST access passed. A subsequent start
with `sslmode=require` accepted a trusted certificate with the wrong server
name. The external three-second deadline stopped the application. The package
failed in 9.474 seconds. The initial archive SHA-256 was
`12b65033f91b3444c32d9d2be2222e9e3a0334ee129ca30293ec7388164c8c28`.
The final test also checks filtered lists and gRPC access before and after restart.

Compiler `084bd768b319d053f2fa4420cb758a4b48524ced` passed
[full CI run 34613469939](https://github.com/jsell-rh/stego/actions/runs/34613469939).
Two fresh builds of that commit produced identical hashes for all 106 generated,
state, and dependency files. The hashes also matched after tests and after the
files were copied to the local checkout. No dependency version changed.

All nine selected race tests passed against the pinned output in 55.543 seconds.
The TLS workflow passed in 6.10 seconds. Companion checks covered database
telemetry, pool cancellation, invalid pool settings, startup deadlines, health,
safe failure records, and the SDK Gateway workflow. The preceding candidate
passed the same checks in 58.747 seconds and contract tests in 1.611 seconds.
Module verification and acceptance and generated-code static checks passed.
These test times do not measure production performance.

The checks ran on 2026-09-11 in Job `db-tls`, namespace
`stego-db-tls-20260911`, with Go 1.26.8 and PostgreSQL 18.6. The test
container had one CPU and a 3 GiB memory limit. PostgreSQL had half a CPU and
a 512 MiB memory limit. The job deadline was 30 minutes. All builds and runtime
checks ran in the cluster. The pinned application archive SHA-256 was
`a25371ec8a9dfabedd09f26543e5e8a9382912d99e23eb963562a2ceee8a5db8`.
Final local edits changed this evidence document only. Full application CI,
including its Docker TLS fixture setup, remains a separate gate.
