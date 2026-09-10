This request-signal gate first used compiler
`f89bf13e7a65c1a6224a31704cec858c8d254ddf`. The later
[service logging gate](service-logging.md) records the current pin and checks.

Hypershell uses the shared STEGO request observability runtime for HTTP and gRPC.
Component `otel-tracing` 1.2.0 adds OpenTelemetry request logs and metrics to the
existing spans. The application has no exporter, metric registry, or request-log
wrapper of its own. The runtime uses one verified TLS collector connection and
separate bounded providers for the three signals.

`TestGatewayLogsMetricsAndTracesAcrossRestart` runs the generated API with
PostgreSQL and a local TLS OTLP collector. It opens an authorized watch, creates
a Gateway over gRPC, receives its event, and reads the Gateway through REST.
It also sends a request without credentials. Completed calls must produce logs
that match their spans, including trace and span IDs, request fields, and service
identity. Serialized export data must exclude Gateway IDs, names, tokens,
baggage, and tracestate.

A periodic metric sample must show the live watch as active. The watch must not
have a completion log or span until it ends. After cancellation and API shutdown,
the active count must return to zero, and HTTP and gRPC duration counts must each
include both calls. After API restart, an owner read must produce a correlated
log, span, and metric exemplar. After the collector stops, Gateway access and
bounded API shutdown must still work.

The old compiler served the workflow but did not export the required request
logs. That baseline failed in 7.96 seconds. The first local implementation passed
the complete workflow in 27.30 seconds under race detection. The gate uses the
default ten-second metric interval, including a new interval after restart.
This elapsed time is a test duration, not an application latency measurement.

Set the existing `OTEL_EXPORTER_OTLP_ENDPOINT` to an HTTPS collector origin to
export all three signals. `OTEL_METRICS_EXPORTER=none` or
`OTEL_LOGS_EXPORTER=none` disables that signal for a collector that does not
support it. Both default to `otlp` when the endpoint is set.
`OTEL_METRIC_EXPORT_INTERVAL` accepts 1,000 through 60,000 milliseconds.
The default is 10,000. Other supported transport and sampling settings are in
[HTTP tracing](http-tracing.md).

Request duration histograms use seconds and cumulative counts. The names are
`http.server.request.duration` and `rpc.server.call.duration`. Active-request
counters have no labels. Histograms have a fixed 128-series limit, with overflow
counts when more field combinations occur. Request logs use fixed messages and
safe fields. Metrics and logs still cover requests whose spans are not sampled.
Export is best effort, with bounded queues, no retry, safe failure counters, and
one shared three-second provider shutdown deadline.

The [compiler contract](https://github.com/jsell-rh/stego/blob/f89bf13e7a65c1a6224a31704cec858c8d254ddf/specs/request-observability.md)
records fields, bounds, dependencies, and the RPC metric name change from the
older upstream convention. This request workflow does not complete full service
logging, controller and database instrumentation, outbound tracing, runtime
metrics, or production capacity. Those remain shared STEGO requirements.


With the pinned compiler, all nine selected application workflows passed under
race detection with PostgreSQL required in 67.981 seconds. The combined signals
workflow passed in 23.73 seconds. The other checks cover REST and gRPC access,
health under database delay, HTTP and gRPC tracing, durable events, watch expiry,
watch-source failure, and restart. Internal and contract race tests and static
checks passed. The dependency scan reported no vulnerabilities.

Repeat generation preserved all 93 generated, state, and dependency file hashes.
The new generated file is `out/tracing/signals.go`; tracing runtime, compiler
state, build record, and dependencies also changed. Full application and
provider gates were not repeated locally for this change. Full remote CI must
produce its own result at the new revision.
