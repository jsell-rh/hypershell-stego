# Browser startup signals

This candidate pins the compiler and common registry to
`7cd7f68b6c4f48cf002934e838085f4b15257d61` in all three modules. The generated
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
required archive file. Full compiler CI and application workflow qualification
are still required. This candidate does not establish the cause of the earlier
CNPG browser startup failure or replace the qualified dashboard evidence.

The [Gateway console module check](dashboard-startup-module-evidence.json)
passed at `a55618b`. All 130 archived source files match the candidate. The
published image binary matches the checked module build. All 134 downloaded Go
module files also match, including the inherited repository license. The API
now uses that checked module revision for the console deployment and schema
packages. This result does not qualify a running dashboard or the full compiler.
