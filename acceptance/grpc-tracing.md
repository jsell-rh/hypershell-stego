The variant uses STEGO compiler `e68f200ef638069ef18654c728c702a60a8507aa`,
`otel-tracing` 1.1.0, and `grpc-application` 1.8.0 for shared HTTP and gRPC tracing.
The generated main program passes one tracing runtime to both transports.
Hypershell has no separate span exporter, metadata parser, or stream observer.

`TestGatewayGRPCTracingAcrossWatchRestartAndCollectorFailure` runs the generated
API with PostgreSQL and a local TLS OTLP collector. It opens an authorized watch,
creates a Gateway over gRPC, and checks the resulting watch event. The expired
watch must produce a span for its whole lifetime with `DEADLINE_EXCEEDED` status.
The test also checks missing credentials, a read by another user, successful
owner retrieval after restart, and continued access and bounded shutdown after
the collector stops.

The exported spans must retain the declared service identity and caller trace
and parent IDs. They must use registered protobuf method names and fixed gRPC
status fields. Gateway IDs, names, tokens, baggage, and tracestate must not occur
in exported data. The test checks the serialized OTLP request for these values.
It also checks the failed status and duration of the expired watch.

The previous compiler created the Gateway and delivered the event, but exported
no gRPC span. The baseline failed in 7.78 seconds. The new local compiler passed
the workflow in 8.48 seconds under race detection. The full compiler race suite
passed with PostgreSQL required; static checks also passed.

The [compiler contract](https://github.com/jsell-rh/stego/blob/e68f200ef638069ef18654c728c702a60a8507aa/specs/grpc-tracing.md)
records the span fields, status policy, and limits. The existing
[HTTP exporter settings](http-tracing.md) apply to both transports. Only calls
that reach registered server interceptors produce these spans. Pre-interceptor
transport failures, unknown methods, client calls, database operations, and
controller actions need separate instrumentation. Full observability compliance
and production capacity remain open.

With the pinned compiler, eight selected application workflows passed under race
detection with PostgreSQL required in 41.629 seconds. The new gRPC tracing
workflow passed in 7.68 seconds. The other checks cover HTTP tracing, health
under database delay, REST and gRPC access, durable event delivery, restart,
watch expiry, and fatal watch-source failure. All internal and contract race
tests and static checks passed. Repeat generation preserved all 92 generated,
state, and dependency file hashes. Module dependencies did not change.

The full application suite and separate Keycloak, Kubernetes, and VM gates were
not repeated locally for this change. Full remote CI remains required. A selected
workflow result does not replace that full result.
