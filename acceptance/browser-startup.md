# Browser startup signals

This candidate pins the compiler and common registry to
`83592bee5a17de6936cf521b94629e2a225a8d37` in all three modules. The generated
browser constructors use STEGO's process telemetry runtime. No application
startup logger or provider is added.

The browser workflow requires all eight startup stages to have matching logs
and spans, duration metrics, and a zero active-stage count. The management
console checks use the exact pool runtime identity before restart, after
restart, and after session key rotation. The public dashboard check uses the
relay runtime identity attached by STEGO to its rendered document logs,
traces, and metrics. Browser input cannot set that identity.
Signals from another process cannot supply a missing startup record.

The collector checks startup records after authentication. It accepts only
fixed stages, outcomes, messages, and fields. It retains bounded correlation
IDs and metrics. It rejects unknown fields, invalid IDs, and excess records.
It does not retain complete OTLP payloads for this check. The result is saved
as `browser-startup-signals.json`. A successful cluster browser test must
include this file in its evidence archive.

All three modules passed repeated local generation and drift checks with a
clean compiler build. Small local tests cover missing records, process identity
separation, repeated export, private fields, invalid signal types, and the
required archive file. All six compiler jobs passed in
[35180190820](https://github.com/jsell-rh/stego/actions/runs/35180190820).
Native logout passed in
[35180190817](https://github.com/jsell-rh/stego/actions/runs/35180190817).
Source-specific application results are below. This candidate does not establish
the cause of the earlier CNPG browser startup failure.

The [Gateway console module check](dashboard-startup-module-evidence.json)
passed at `eacee52`. All 130 archived source files match the candidate. The
published image binary matches the checked module build. All 134 downloaded Go
module files also match, including the inherited repository license. The API
now uses that checked module revision for the console deployment and schema
packages. This result does not qualify a running dashboard.

The [rendered management workflow](startup-management-workflow-evidence.json)
passed at `bf83eef` with compiler `83592be` in 96.89 seconds. Its three process
instances each exported all eight startup stages. All 24 log/span pairs match;
duration metrics are present, and all active-stage counts returned to zero.
The test binds these checks to the pool identity before restart, after restart,
and after key rotation. Both saved management screenshots were inspected. This
result does not replace the separate live public Gateway dashboard test.

The [complete public workflow](startup-public-workflow-evidence.json) then passed
at `bf83eef` in 667.64 seconds. All 1,351 captured source files match that commit.
All 415 generation hashes match before and after the workflow and in the saved
archive. The Gateway console image contains the qualified module binary.
Three dashboard screenshots were inspected. The active workspace, invalid JSON
denial, syntax colors, and text selection are visible.

The management console and Gateway console each supplied three observed runtime
instances. All six have eight matching startup log/span pairs, duration metrics,
and zero active-stage counts. No failed stage pair is present in that record.
The test requires dashboard document signals and startup signals from the same
relay instance. SQL isolation, controller recovery, filtered access, denied
requests, account use and deletion, and final Gateway deletion passed. The
supplied PostgreSQL server and installation data remained. Independent cluster
cleanup passed at `2026-09-17T04:30:02Z`; no test workload or allocation remained,
and the shared test lease was empty. This qualifies the public workflow at the
named source. The complete CNPG workflow still needs its separate result. The
earlier recovered CNPG startup cause remains unknown.

The automatic API run `35178368373` at the earlier main revision stopped during
regeneration. Its read-only Pod had no writable user cache setting, so the Git
registry tried to create `/.cache`. Two setup tests passed; 49 required checks
were not run. The Job and private fixtures were removed, and the test lease
was released. The shared test Job now sets `XDG_CACHE_HOME=/work/cache` inside
its existing writable volume. The root filesystem remains read-only. A new
API cluster run was completed at `9ac3c73` in
[35180081993](https://github.com/jsell-rh/hypershell-stego/actions/runs/35180081993).
All 51 required tests passed. All 415 generation hashes match before and after
regeneration, after the tests, and in the saved archive. All 1,351 source hashes
match that commit. The runner then lost the response to its collection
acknowledgement. It did not verify that the Job was terminal. The workflow is a
failure, although independent cluster cleanup passed. The
[saved evidence](api-startup-run-evidence.json) retains that distinction.

The runner now saves test results before it acknowledges collection. It checks
the original Job UID and retries only the idempotent acknowledgement if its
response is lost. It must observe a matching terminal Job condition. If it
cannot verify completion, it retains the Job, fixture, and lease for inspection.
The recovery checks cover lost writes, lost responses, delayed completion,
replacement Jobs, read failures, and deadlines. Fourteen observation tests and
eight credential tests passed locally. The [live API recovery result](api-startup-recovery-evidence.json)
then passed at `8c37bb2` in run `35182200900`. All 51 required tests passed.
All 1,354 source hashes match that commit, and all 415 generated-file hashes
match the four snapshots and saved archive. The saved original Job reached
`Complete`. Independent cleanup at `2026-09-17T04:40:59Z` found no test
resources, allocations, or held lease. This qualifies the runner in the complete
API workflow. Transport-loss cases also retain their focused test coverage.

The [full application check](startup-core-workflow-evidence.json) passed at
`bf83eef`. The core job passed 307 top-level tests and 651 checks with subtests.
Four named live tests were excluded from that job. The rendered management
browser, web console, and service image jobs also passed. CNPG and Sandbox were
not selected; this result does not supply their missing evidence.

The [CNPG startup run](startup-cnpg-failure-evidence.json) at `c0c2d23` failed
in run `35182853953`. Its complete browser test stopped after 644.44 seconds.
CNPG primary replacement and namespace recovery passed before that failure.
The first automation account in the final deletion setup received HTTP 409
with `gateway_not_ready`. The saved current workload observation was neither
healthy nor running.

The generated RPC logs contain six `GetCredentials` calls with `UNAVAILABLE`
during the test's deliberate provisioner restart. Calls then recovered. The
provisioner process readiness check did not wait for Gateway controller
recovery. The test proceeded to account creation while the controller's
unavailable observation was still current. This record explains this run;
it does not establish the cause of older failures.

All 1,356 captured source hashes match the failed revision. All 415 generation
hashes match the two snapshots before the test and the saved generated archive.
The test did not reach the final generation snapshot, complete startup signal
record, or Gateway account deletion check. This is a failed gate. Independent
cleanup at `2026-09-17T05:07:14Z` found no test workloads, allocations, volumes,
or held lease. One secondary Pod replacement was needed for scheduling; the
primary and storage identities were preserved. Unattended scheduling remains
unverified.

The revised test holds the provisioner down until both Gateways have current
unavailable observations. It requires HTTP 409 for account creation and no
account rows from those denied requests. After provisioner restart, it waits
for both Gateways to become healthy and verifies retained SQL and credential
identities. Account writes are not retried. Successful public workflow evidence
must contain `provisioner-restart.json`. Production code and readiness rules
are unchanged. The focused diagnostic check and evidence collection cases
passed locally. Complete workflow qualification is still required.

The [next CNPG run](startup-cnpg-collection-failure-evidence.json),
`35184753568` at `fdd11d3`, passed all 11 application tests. The complete
browser test took 742.70 seconds. Its log confirms the deliberate provisioner
outage and controller recovery check. All 1,358 source hashes match the commit.
The original Job reached `Complete`, but the outer runner failed during evidence
collection. The downloaded archive has zero bytes. This is a failed gate.
The saved output does not identify a missing file or a transport failure.
Final generation, browser images, and telemetry records cannot be verified.
Independent cleanup at `2026-09-17T05:36:59Z` found no test resources,
allocations, volumes, or held lease. No scheduling intervention was required.

The collector now reports each missing or empty required file. It creates an
archive of the available evidence even if a required file is absent. Transfer
failures report their exit status. Missing evidence still fails the gate.
Small local tests cover missing, empty, and multiple missing files, retained
archives, transport failures, and truncated archives. The CNPG workflow runs
these checks before cluster use. Complete CNPG qualification remains open.
