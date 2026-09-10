STEGO compiler `f87dfaf0d06cde2f6eeea1b5145edbd9e568f1c1` adds shared HTTP
client logs, metrics, spans, and trace propagation. The variant uses
`http-application` 1.5.0, `cli-application` 1.5.0, and `otel-tracing` 1.8.0.
Keycloak policy and API mappings stay in the domain provider. No HTTP telemetry
wrapper was added to domain code.

The standalone provisioner now owns a generated telemetry runtime and supplies
its gRPC trace boundary to the generated server. Server shutdown and provider
cleanup occur before telemetry closes. Incoming provider calls give the common
HTTP client its runtime through context. Each process keeps its own service
instance ID. Collector settings remain deployment configuration.

`TestKeycloakHTTPClientTelemetryAcrossRestart` uses real Keycloak, PostgreSQL,
the generated API process, the generated RPC transport, and a TLS OTLP collector.
It creates a service account, verifies its credential, checks a denied read,
and requires Gateway event delivery. It then restarts the API and provider,
reads retained account state, revokes the account, and checks that the credential
can no longer obtain a token.

Both phases require the actual trace chain from API request or recovery work
to RPC client, provider RPC server, and HTTP client. The test requires the
`Provision` method during creation and `Delete` during revocation. HTTP logs
must share the span's trace and span IDs. A duration measurement must match its
provider instance, HTTP method, status, and outcome. Local JSON must contain the
same completion. The provider instance must change after restart. Account IDs,
account names, credentials, and caller tokens must be absent from collected
signals and process logs.

The first probe on compiler `d771730` failed after successful account creation:
no generated HTTP client signals were present. After implementation, a test
assertion required correction: completed revocation returns HTTP 200; pending
revocation returns 202. The test accepts these contract results and requires the
retained state to reach `revoked`. No API behavior changed for that correction.

Twenty selected PostgreSQL and Keycloak acceptance tests passed under race
detection in 152.410 seconds. The new HTTP client workflow passed in 26.60
seconds. The existing real Keycloak workflow passed in 34.09 seconds, including
role reduction and restart recovery. The selection also covered atomic Gateway
writes, rollback, filtered access, REST/gRPC parity, CLI calls, durable events,
request and controller signals, process failure privacy, and restart.
Internal and contract race tests passed. Static checks passed. Repeat generation
preserved all 99 output, state, and dependency hashes.

The full STEGO race suite passed with PostgreSQL 18.6 required. Compiler CI
[34539473482](https://github.com/jsell-rh/stego/actions/runs/34539473482) passed.
Full variant CI must report its own result. The Kubernetes and VM provider gates
were not repeated locally for this HTTP client change.

The [compiler contract](https://github.com/jsell-rh/stego/blob/f87dfaf0d06cde2f6eeea1b5145edbd9e568f1c1/specs/http-client-observability.md)
defines privacy, bounded export, cancellation, and stream completion. It also
records the performance measurements and their limits. Database telemetry,
independent CLI runtime startup, complete process logging, and production
capacity evidence remain open.
