The Gateway workload worker now receives PostgreSQL operation telemetry through
STEGO's private runtime. The client no longer uses global providers or the
default logger. Hypershell adds no exporter, log handler, or operation wrapper.
The existing controller context connects the generated SQL client to the
generated worker runtime.

The complete browser workflow now requires correlated SQL logs and traces,
linked to controller parent spans, plus operation metrics. Successful `ensure`
operations must come from both Gateway worker instances, before and after Pod
replacement. The same run must contain failed and successful `delete`
operations while it proves SQL cleanup denial, retained state, and recovery.
These signals use the worker's service and instance identity.

The collector retains bounded correlation records and fixed operation outcomes.
It rejects undeclared attributes in PostgreSQL signals. The evidence file is
`browser-artifacts/postgres-signals.json`. It records only runtime instance IDs
and the operation outcomes with complete trace, log, and metric evidence. A
unit check verifies that a missing log or controller parent cannot establish
correlation and that an undeclared SQL field fails the check.

The shared contract is
[PostgreSQL client observability](https://github.com/jsell-rh/stego/blob/main/specs/postgres-client-observability.md).
Repeated generation and drift checks passed with compiler `f2b09c0`. Bounded
acceptance compilation and the collector regression passed. The full browser
and API workflows remain required for this source. A compiled test is not a
passing application result.

[Full compiler CI](https://github.com/jsell-rh/stego/actions/runs/34961995199)
passed at `f2b09c0`, including race checks and PostgreSQL provisioning. The
complete Hypershell API and browser results remain required for this change.

The [API evidence](postgres-telemetry-api-evidence.json) now records a pass at
`2af1b46` in run `34962176232`. All 31 required tests passed in 153.598 seconds.
Verification matched 902 source files, 230 generated files, and all generation
records. Automatic and independent cleanup checks passed. The complete browser
SQL signal check remains required. The later worker startup change needs its
own compiler and application results.
