STEGO compiler `8ad255a4bcfc877c6bcb77a53586cc0abaf1fe00` supplies telemetry
at the generated CLI entry point. The variant uses `cli-application` 1.6.0
and `otel-tracing` 1.9.0. Each command process owns one runtime. Hypershell
supplies command definitions; no domain telemetry wrapper is required.

The runtime supplies command logs, duration and active-command metrics, and a
root span. Generated HTTP clients continue the trace into the API. Diagnostic
JSON goes to stderr. Command JSON stays on stdout. The version test reads these
streams separately and requires a command completion record.

`TestGeneratedCLIObservabilityAcrossRestart` runs the generated CLI and API
with PostgreSQL, a TLS API endpoint, a broker fixture, and a TLS OTLP collector.
It creates a Gateway and checks its owner grant and event delivery. A caller
without access must receive the opaque HTTP 404 response. After API restart,
the owner must read the same Gateway. The command must also return that Gateway
within six seconds when its collector is unavailable.

Each CLI invocation must produce one local completion record and a distinct
runtime instance ID. Successful and denied API calls must have a complete
command-to-client-to-server trace, a correlated command log, and a duration
measurement. Gateway IDs, private names, and the owner token must be absent
from diagnostic output and exported signals.

The first probe on compiler `f87dfaf` failed after successful login because
the CLI had no common completion record. The compiler now provides that
behavior. The [compiler contract](https://github.com/jsell-rh/stego/blob/8ad255a4bcfc877c6bcb77a53586cc0abaf1fe00/specs/cli-observability.md)
defines cancellation, privacy, export limits, and performance evidence.
Compiler CI [34555494184](https://github.com/jsell-rh/stego/actions/runs/34555494184)
passed for that revision. The variant requires a separate full CI result.

Twelve selected acceptance tests passed under race detection in 77.043 seconds
in the OpenShift `jshell` cluster. The new CLI telemetry test passed in 11.74
seconds. The selection included Gateway atomic writes, rollback, filtered
access, REST/gRPC behavior, CLI calls, event delivery, telemetry, task failure,
and restart. Two generation passes preserved all 100 output, state, and
dependency hashes.
Internal and contract race tests passed. Static analysis passed. The job
finished with exit code 0. The downloaded files matched all 100 recorded hashes.

Job `cli-integration` ran in the dedicated namespace `stego-test-20260910`.
The source archive SHA-256 was
`af6c8a67bb214a120b4ca4654d3a69ce586f9349ffc100819e42d0690eb86484`.
It used Go 1.26.8 and PostgreSQL 18.6. The test container had a one-CPU limit,
a 3 GiB memory limit, and `GOMAXPROCS=1`. The database had separate limits of
half a CPU and 512 MiB. The job had a 30-minute deadline. It received no
service-account token or workstation credentials. PostgreSQL listened only on
the Pod's loopback interface. No performance test ran on the workstation.

Shared [database signals](database-observability.md) now have a separate Gateway
workflow. Other independent worker entry points, complete process logging,
and production capacity evidence remain open.
