The first HTTP tracing gate used STEGO's `otel-tracing` component at version
1.0.0. Its compiler pin was `164c7dc25d4bee5ecb794876f92e762edf6a0c44`. The application
has no exporter, span queue, response wrapper, or trace-context parser.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to an HTTPS collector origin to enable
OTLP/gRPC tracing. `OTEL_EXPORTER_OTLP_PROTOCOL` must be absent or `grpc`.
`OTEL_EXPORTER_OTLP_CERTIFICATE` can select a PEM trust file. System roots apply
otherwise. `OTEL_SERVICE_NAME` selects the service name; the default is the
service declaration's name. `OTEL_TRACES_SAMPLER_ARG` selects the local sampling
ratio from zero through one; the default is 0.1. Other nonempty `OTEL_` settings
are rejected. No endpoint means no tracing provider or request wrappers.

Each HTTP span records method, registered route pattern when available, and
status. For example, an authorized Gateway read produces
`GET /api/hypershell/v1/gateways/{id}`. It does not put the Gateway ID in the span
name or route attribute. Denied requests also produce spans. Raw URLs, query
values, bodies, credentials, user details, baggage, and tracestate are not copied.
A valid supported traceparent continues the caller's trace and parent ID.
The local sampling ratio applies even if a remote caller requests sampling.

The generated queue has 256 slots and batches of at most 32 spans. Export uses
verified TLS 1.3, no environment proxy, a two-second deadline, and bounded RPC
messages. Queue pressure can drop traces but cannot block an API request.
Provider shutdown has a three-second budget. Failed exports have fixed error
text and a batch-failure counter. This is best-effort telemetry; it has no
loss-free or durable-delivery guarantee.

`TestGatewayHTTPTracingAcrossRestartAndCollectorFailure` uses PostgreSQL, the
actual generated API, and a local TLS OTLP collector. It verifies successful
and denied reads, declared service identity, trace and parent IDs, route and
status fields, private-data exclusion, API restart, and continued access after
collector loss. The original baseline served the request but exported no trace.

With the pinned compiler, all seven selected acceptance workflows passed under
race detection in 35.435 seconds with PostgreSQL required. They cover tracing,
health after database delay, REST and gRPC Gateway access, event delivery across
restart, watch expiry, and fatal watch-source shutdown. A later focused check
also verified the exact service resource and parent fields. All internal and
contract race tests and static checks passed. The dependency vulnerability scan
reported no vulnerabilities. The full compiler race suite passed with
PostgreSQL required.

Generation adds `out/tracing/runtime.go` and updates the main program, compiler
state, build record, and module dependencies. The generated dependencies include
OpenTelemetry 1.46.0, gRPC 1.83.1, and protobuf 1.36.12. This change does not repeat
the full application suite or the separate Keycloak, Kubernetes, and VM gates
locally. Full remote CI remains required.
Repeated pinned generation preserved all 92 output, state, and dependency hashes.

The [compiler contract](https://github.com/jsell-rh/stego/blob/164c7dc25d4bee5ecb794876f92e762edf6a0c44/specs/http-tracing.md)
records limits and an isolated wrapper measurement. It also records deliberate
sampling and baggage differences from the upstream observability specification.
The later [gRPC tracing gate](grpc-tracing.md) adds server calls and watch spans.
Database spans, request metrics, controller traces, console trace
continuation, OTLP/HTTP, and collector client authentication remain open. This
HTTP workflow does not complete the observability or full Hypershell goal.
