The generated worker callback now owns STEGO telemetry before domain setup.
Hypershell adds no runtime, exporter, or logging wrapper. Setup, reconciliation,
and deferred cleanup use one runtime instance. A callback failure emits the
fixed `service.failed` event before the runtime closes.

The startup acceptance check now covers namespace allocation, Gateway identity,
Gateway workload, and Sandbox count workers. It starts each generated binary
with invalid private provider configuration and a real TLS OTLP collector. Each
process must return exit code 1, exclude private configuration from output, and
emit exactly one runtime start, failure, and runtime stop event. Local and OTLP
records must share one valid service instance ID. The existing fixed local
process failure record remains required.

The Gateway workload regression failed with compiler `f2b09c0`: no runtime
lifecycle or failure event reached local output or OTLP during provider setup.
The update adopts compiler `f97b315`. Repeated generation and drift checks pass.
The focused four-worker check passes with local and TLS OTLP evidence in 3.887
seconds. [Full compiler CI](https://github.com/jsell-rh/stego/actions/runs/34964671704)
passed at `f97b315`, including controller race checks and SQL provisioning.
Complete API and browser workflows remain required for this source.

Hypershell `677973f` has queued API run `34964891352`, browser run `34964891373`,
and full CI run `34964891409`. These checks retain the complete Gateway workflow
and the SQL telemetry requirements from the previous source.

This does not establish complete API or RPC bootstrap telemetry. It does not
declare the worker ready before its actual queue and provider checks succeed.
See the [shared worker contract](https://github.com/jsell-rh/stego/blob/main/specs/worker-startup-observability.md).

The [complete API evidence](worker-startup-api-evidence.json) records a pass for Hypershell `677973f`
in run `34964891352`. All 31 required tests passed; acceptance took 166.615
seconds. This run includes the four actual worker startup failures and their
correlated local and TLS OTLP records, in 7.24 seconds. Verification matched
907 source files, 230 generated files, and four identical generation records.
Automatic cleanup and independent cluster reads found no test resources. The
browser result is recorded below.

The [complete browser evidence](worker-startup-browser-evidence.json) records a pass for
run `34964891373` at the same source. The workflow took 398.22 seconds. It
verified 907 source files, 231 generated files, three equal generation records,
worker replacement telemetry, SQL isolation and recovery, access rules, account
cleanup, encryption, and session behavior. The four worker startup checks passed
in 6.44 seconds. Automatic cleanup and independent reads found no browser test
resources. The Gateway page shows Healthy; its public connection panel still
shows loading placeholders. Public Gateway connectivity remains unverified.

Full CI `34964891409` passed core acceptance, ordinary browser tests, console,
and service image. Its overall result is failure: CNPG and Sandbox jobs require
installation fixtures and restricted runners. These gates remain required.
