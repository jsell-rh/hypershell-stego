The variant pins compiler `19b9e74c21ff5b03d5f8d763ec6df7cfdfcf2b3b`. Its
constructor validation rejects a dependency name when more than one component
can produce that value. It no longer selects a producer from component order.

Gateway tracing exposed this shared compiler requirement: event delivery and
tracing formerly used `NewRuntime`. The tracing runtime now has a distinct
assembly name. HTTP and gRPC use that same tracing instance, while the event
worker keeps its own runtime. This compiler change adds no Hypershell rule.

The [compiler regression](https://github.com/jsell-rh/stego/blob/19b9e74c21ff5b03d5f8d763ec6df7cfdfcf2b3b/specs/constructor-dependency-identity.md)
fails against the previous compiler. The corrected full compiler race suite
passed with PostgreSQL required. Static checks passed.

Pinned regeneration changed only saved compiler identity and the CLI build
record. All other generated source and dependencies stayed unchanged. Repeat
generation preserved all 92 generated, state, and dependency file hashes.
Five selected application checks passed under race detection with PostgreSQL
required in 22.458 seconds. They cover the clean CLI compiler identity, REST and
gRPC Gateway access, HTTP and gRPC tracing, watch delivery and expiry, event
delivery, restart, and collector failure. Static checks passed.

These selected checks do not replace full application CI or the separate
provider jobs. The full remote run remains required. Full shared OpenTelemetry
logs, metrics, and traces are also required by the user; the current tracing
runtime is only part of that scope. The
[shared requirement](https://github.com/jsell-rh/stego/blob/19b9e74c21ff5b03d5f8d763ec6df7cfdfcf2b3b/specs/shared-observability.md)
requires common instrumentation in STEGO and deployment-only export settings.
