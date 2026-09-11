STEGO supplies database source selection and mounted-file checks. Hypershell
adds no production file reader or environment wrapper. Set `DATABASE_URL_FILE`
to an absolute path and leave `DATABASE_URL` empty. Files must be regular and
no larger than 64 KiB. Use owner-only permissions or read-only group access.
Other users must have no access. Modes `0400`, `0600`, `0440`, and `0640` meet
the permission policy. The existing verified TLS policy still applies.

For Kubernetes, mount the Secret directory with `defaultMode: 0440` and the
service's private volume group. Do not use a `subPath` mount if the file must
receive updates. Point `DATABASE_URL_FILE` to the selected key in that directory.
The process reads the file at startup. Update the file and restart the process
after a credential change. This contract does not supply full deployment
packaging or rotation without restart.

`TestGatewayDatabaseSecretFileAndCredentialRotation` creates a separate database
login with table and sequence grants for the test database. It uses a projected
file layout and verified PostgreSQL TLS. The fixture must reject an invalid
password with PostgreSQL code `28P01` before the workflow starts. CI initializes
TCP authentication with `--auth-host=scram-sha-256`. The generated process creates a
Gateway, commits the owner grant, and delivers the event. Pool and LISTEN
sessions must use TLS. REST and gRPC reads must succeed for the owner and deny
an unrelated user.

The test stops the process and changes the login password. Startup with the
old file must fail at `database.ping`. It then replaces the projection with the
new credential. Conflicting environment and file sources, missing files, FIFOs,
publicly readable files, and large files must fail at `database.configure`.
Failure records must exclude credentials, database and role names, and paths.
A final restart must read the original Gateway and create another Gateway with
its owner grant and delivered event. The companion TLS and SDK gates check
filtered lists and transaction rollback.

The compiler also checks a real mounted Kubernetes Secret in the bounded
cluster job. Full application CI and fresh compiler regeneration are separate
gates. This test does not establish production capacity or a complete deployed
control plane. The wider enterprise goal remains open.

The first probe used compiler `084bd76`. The process rejected file-only
configuration at `database.configure` before it opened an API listener. The
package failed in 5.339 seconds. The initial application archive SHA-256 was
`1040bd4147a45bae25d7c9f261e6368b2e417e67ab8ea213242390028293a053`.

The first candidate passed file-configured creation, owner grants, event
delivery, REST and gRPC access, and TLS session checks. The password-change
check then failed in 6.96 seconds. The cluster fixture trusted loopback TCP
connections and did not check the password. Its host rules were changed to
SCRAM, and an independent invalid-password probe confirmed rejection before
the next run. The failed result was retained; it was not counted as a pass.

Compiler `97343088ab72743bbc8fd8a6695e379d16c6e183` passed
[full CI run 34615392503](https://github.com/jsell-rh/stego/actions/runs/34615392503)
with SCRAM authentication in the database fixture. Two fresh builds of that
commit produced identical hashes for all 106 generated, state, and dependency
files. The hashes still matched after tests and after copying output to the
local checkout. No dependency version changed.

All ten selected application race tests passed against the pinned output in
63.032 seconds. The secret-file and credential-change workflow passed in
6.21 seconds. Companion checks covered database telemetry, pool cancellation,
invalid pool settings, startup deadlines, verified TLS, health, safe failure
records, and the SDK Gateway workflow. The preceding SCRAM run passed the same
group in 62.458 seconds and contract tests in 1.547 seconds. Static checks and
module verification passed. These times do not measure production capacity.

The checks ran on 2026-09-11 in Job `db-secret`, namespace
`stego-db-secret-20260911`, with Go 1.26.8 and PostgreSQL 18.6. The test
container had one CPU and a 3 GiB memory limit. PostgreSQL had half a CPU and
a 512 MiB memory limit. The job deadline was 30 minutes. All builds and runtime
tests ran in the cluster. The pinned application archive SHA-256 was
`db05c735100f8b79560151ebe5b8bee7547cdb263fc257602a8b4a3326053b49`.
Final local edits changed this evidence document only. Full application CI
remains a separate gate.
