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

The [first run](../../acceptance/dashboard-source-first-evidence.json) failed.
The Go build passed, but its vulnerability check found an affected gRPC call.
The JavaScript audit found affected router dependencies. UI types and build
passed; the resulting assets failed STEGO's bundle contract.

The checked build inputs now select gRPC 1.83.2 and React Router 7.18.0. The
small source patch removes the earlier router flags, whose behavior is included
in version 7. CI checks the existing authentication and Sandbox list tests.
The original upstream commit and the modified build tree are recorded separately.
These files change the build inputs; they do not copy the dashboard backend
into Hypershell or replace its implementation. Qualification remains pending.

The production asset configuration keeps the upstream entry point and API.
It emits assets under `/assets/`, splits JavaScript chunks, and emits imported
CSS as separate files. It preserves license comments in JavaScript and omits
source maps from the served files. The HTML uses local assets and the existing
font fallback. This does not yet prove the editor's runtime styles or the full
browser content policy. The rendered editor and terminal checks remain required.

This check does not deploy the dashboard or prove authentication, rendering,
the editor, terminal access, restart, or cleanup. Those remain required in the
complete Gateway workflow. Do not weaken browser security rules to make the
source check pass. Use its failures to select the next integration change.

See the [application gap](../../acceptance/per-gateway-console-gap-20260915.md)
and [source check](../../.github/workflows/dashboard-source.yml).
