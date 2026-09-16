# Upstream Gateway dashboard

Keep the upstream OpenShell dashboard API and UI. STEGO must supply its browser
authentication, telemetry, deployment, and lifecycle. Hypershell supplies Gateway
access rules and configuration.

The first source check uses upstream revision
`07f1b13ebd9e1826afe943b831b092be8bf92498`. It checks the Go build and known
vulnerabilities, locked JavaScript dependencies, UI types and build, and the
actual asset output against STEGO's bundle contract. It runs in bounded CI.
Each check must pass; an earlier failure cannot hide behind artifact collection.
The source archive, dependency inputs, logs, and outcomes are retained together.

This check does not deploy the dashboard or prove authentication, rendering,
the editor, terminal access, restart, or cleanup. Those remain required in the
complete Gateway workflow. Do not weaken browser security rules to make the
source check pass. Use its failures to select the next integration change.

See the [application gap](../../acceptance/per-gateway-console-gap-20260915.md)
and [source check](../../.github/workflows/dashboard-source.yml).
