The compiler pin is `86b436c27bd31817733dd0356d3aea5ca10c8a77`.
Controller 1.13.0 and telemetry 1.4.0 now supply common logs, metrics, and spans
for keyed reconciliation. Hypershell's identity, workload, and database
controllers no longer translate common runtime events into log messages.
Provider actions and domain rules remain in Hypershell.

`TestGatewayControllerTelemetryAcrossFailureAndRestart` runs the generated API
with PostgreSQL, a live identity controller, and a local TLS OTLP collector.
It creates a Gateway through REST and observes a controlled provider failure
through the generated gRPC state API. A retry must produce a correlated work
log and span, a duration histogram, a retry count, and a live queue gauge.
After the provider recovers, the durable condition must become current and
successful work must produce correlated telemetry.

The API then restarts while the controller stays active. Telemetry must show
watch reconnect and a new watch session. After the collector stops, a later
Gateway update must still reconcile and remain readable through REST. Controller
shutdown must remain bounded. Serialized exports must exclude Gateway names,
IDs, credentials, and provider error text. The controlled provider supplies
failure timing; this particular test does not run Keycloak.

With compiler `47a0e0a`, creation and the provider failure condition worked, but
the test failed in 9.46 seconds because controller telemetry did not arrive.
With the implementation at `a026128`, the complete test passed under race
detection in 8.36 seconds.

All nine selected acceptance workflows passed with PostgreSQL and Keycloak
required in 119.076 seconds. They include the real Keycloak identity controller
workflow, which passed in 33.83 seconds, and action and inventory retry delays
across API restart. They also cover independent cleanup progress, durable failure
conditions, combined request telemetry, service logs without a collector, and
watch-source failure. Internal and contract race tests and static checks passed.

Compiler CI run 34532475046 exposed an ordering assumption in an existing
reconnect test. The follow-up compiler commit changes tests and documentation;
production runtime code is unchanged. Four hundred targeted race runs passed
with and without telemetry, under two scheduler settings. Generation at the final
pin preserves every application source hash except the CLI compiler build record.
Compiler state also changes. These comparisons connect the passing application
tests to the final generated runtime. The failed CI run remains a failed result;
the corrected compiler and application revisions need their own remote results.

Repeat generation at the implementation revision preserved all 96 output,
state, and dependency hashes. New generated files are `out/controller/telemetry.go`
and `out/tracing/controller.go`. Existing loopback Prometheus metrics remain in
use. Export setup belongs to the shared runtime and deployment settings.

The [compiler contract](https://github.com/jsell-rh/stego/blob/86b436c27bd31817733dd0356d3aea5ca10c8a77/specs/controller-observability.md)
defines operations, fields, limits, provider ownership, and aggregate queue
metrics. Overlapping controllers in one process share providers. Separate
replicas still need unique telemetry instance identity. Non-keyed sweeps,
domain event declarations, database and outbound spans, and complete process
logging remain open. Full Kubernetes and VM provider gates were not repeated
locally for this change.
