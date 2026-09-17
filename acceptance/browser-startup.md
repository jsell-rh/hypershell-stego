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
Application workflow qualification is still required. This candidate does not establish the cause of the earlier
CNPG browser startup failure or replace the qualified dashboard evidence.

The [Gateway console module check](dashboard-startup-module-evidence.json)
passed at `eacee52`. All 130 archived source files match the candidate. The
published image binary matches the checked module build. All 134 downloaded Go
module files also match, including the inherited repository license. The API
now uses that checked module revision for the console deployment and schema
packages. This result does not qualify a running dashboard.

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
eight credential tests passed locally. A new live API run must qualify this fix.
