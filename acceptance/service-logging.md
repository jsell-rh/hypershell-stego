The compiler pin is `47a0e0a86672e5c510b15cef4f70ee588ec13c6d`.
Component `otel-tracing` 1.3.0 adds fixed service events, local JSON output, and
OTLP service log export. Hypershell adds no log bridge or exporter. Its generated
telemetry constructor emits one runtime start event and one stop event.

`TestGatewayServiceLogsWithoutCollector` starts the generated API without an
OTLP endpoint. It verifies durable Gateway event delivery and structured runtime
logs. The records must include the configured service name, time, and severity.
They must exclude the Gateway name and ID. The old compiler `f89bf13` delivered
the event but failed this check because the logs were absent. That baseline
failed in 3.52 seconds. The new pinned compiler passed in 2.59 seconds.

The [combined observability workflow](request-observability.md) also checks local
and exported runtime events. The first process must export one start and one
stop event. Restart must export a second start event. Both processes must emit
their own local start and stop records, including when the collector has stopped.
Runtime events have no request IDs or arbitrary attributes. Existing request
log, trace, metric, access, watch, event, and restart checks still apply.

All ten selected acceptance workflows passed under race detection with
PostgreSQL required in 71.438 seconds. The combined observability test passed in
23.75 seconds. The other checks cover REST and gRPC access, health under database
delay, HTTP and gRPC tracing, durable events, watch expiry, and source failure.
Internal and contract race tests and static checks passed. No dependency version
changed. Full application, provider, Keycloak, Kubernetes, and VM gates were not
repeated locally for this change. Remote CI must produce its own result.

Repeat generation preserved all 94 output, state, and dependency hashes. The
new generated file is `out/tracing/service.go`. The tracing runtime, compiler
state, and CLI build record also changed.

The [compiler contract](https://github.com/jsell-rh/stego/blob/47a0e0a86672e5c510b15cef4f70ee588ec13c6d/specs/service-logging.md)
defines the fixed event API, bounded local queue, trace correlation, shared
shutdown budget, and failure counters. A blocked stderr write can retain one
worker until the write returns or the process exits. It cannot block event
callers or extend the runtime shutdown deadline. Logs are best effort.

This step does not replace existing plain process messages. Early startup
failure logs, automatic application readiness, domain event declarations,
controller logging, and local request logs remain open shared STEGO work.
