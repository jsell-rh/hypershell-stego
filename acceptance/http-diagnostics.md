Hypershell uses compiler `8ec724da924e265d4a7f196f4deac806227883a2` and
OpenTelemetry component 1.6.0 for HTTP server diagnostics. STEGO selects the
runtime's diagnostic logger during assembly. The application has no custom
diagnostic filter or logging callback.

The generated server replaces raw panic values, stack text, source paths, and
peer details with a fixed `http.server.diagnostic` event. Local output includes
the runtime's service name and instance ID. OTLP export uses the existing log
provider and queue. The [compiler contract](https://github.com/jsell-rh/stego/blob/8ec724da924e265d4a7f196f4deac806227883a2/specs/http-diagnostics.md)
defines logger selection, fallback behavior, resource ownership, and limits.

`TestGatewayHTTPDiagnosticPrivacy` builds the generated application through a
temporary Go overlay. The overlay adds a panic route only to the test binary.
It does not edit generated files or add a production fault switch.

The workflow performs these steps with and without OTLP export:

1. Create a Gateway through REST and verify its committed owner grant.
2. Receive the Gateway event through the generated runtime.
3. Send a private value to the temporary route and trigger a panic.
4. Require the HTTP request to abort, then read the retained Gateway through REST.
5. Stop the process and verify one safe local diagnostic.
6. With export enabled, verify the diagnostic through a real TLS OTLP collector.
   Verify the panic request log, duration metric, and error span as well.

The separate request log and span have the same span ID. The panic metric counts
one request. No signal invents an HTTP status for the aborted request. The server
diagnostic has no trace or span ID because Go's error logger has no request
context. All exported resources match the local diagnostic's runtime identity.
Local and exported data exclude the private panic value, Gateway data, token,
and stack details.

The baseline failed on compiler `6277272` after the complete workflow: stderr
contained the private panic value. With this compiler, both local and OTLP cases
passed. Ten selected application tests passed under race detection with PostgreSQL
required in 65.203 seconds. The other checks cover controller failure and restart,
HTTP and gRPC tracing, combined request signals, startup and database privacy,
local service logs, replica identity, collector failure, and watch-source shutdown.
The controller test includes the forced serialization retry described in the
[CI evidence](ci-evidence.md).

Internal and contract race tests and static checks passed. Repeat generation
preserved all 97 output, state, and dependency hashes. Provider and virtual-machine
workflows were not repeated for this diagnostic-only change; their preceding
results are recorded in the CI evidence. CI must validate the new revision.

Go still constructs its diagnostic internally before the logger discards it.
This test is not a throughput or memory-capacity result. The compiler tests cover
queue pressure, blocked output, and shutdown for both the local fallback and
telemetry runtime. Panics outside the HTTP server, application-owned logs,
complete process lifecycle export, and database and outbound signals remain open.
The full enterprise goal remains active.
