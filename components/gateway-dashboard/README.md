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

The checked build inputs now select gRPC 1.83.2 and React Router 7.18.4. The
small source patch removes the earlier router flags, whose behavior is included
in version 7. CI checks the existing authentication and Sandbox list tests.
The original upstream commit and the modified build tree are recorded separately.
These files change the build inputs; they do not copy the dashboard backend
into Hypershell or replace its implementation. The source build has now passed. Deployment qualification remains pending.

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

The [second run](../../acceptance/dashboard-dependency-evidence.json) passed the
Go build and vulnerability check. It found a newer router advisory, so the
candidate now uses 7.18.4. The tests stopped before execution because jsdom did
not supply `TextEncoder`. The test setup now supplies Node's standard encoder
and decoder. No application authentication or router behavior is mocked by
this change. The tests and complete asset check must still pass in CI.

The [next run](../../acceptance/dashboard-packaging-evidence.json) passed both
dependency checks, UI types and build, and all eight selected router tests.
The remaining failure was STEGO's former 1 MiB captured ZIP limit. The compiler
candidate now has typed input limits: 4 MiB for assets, 1 MiB for protocol and
callback files, and the existing 8 MiB combined limit. The build-only compiler
pin is in `compiler-revision`; it does not change the main application's pin.
The next CI run must capture the actual assets twice, generate a fresh browser
backend, check repeated generation, and build that backend. The small
`generation-service.yaml` is a compiler check, not a deployment declaration.

The [complete source run](../../acceptance/dashboard-assets-evidence.json) passed
all five checks. It captured 35 files (4,702,076 expanded bytes) in a 1,169,044-byte
ZIP. Repeated capture was identical. A fresh generated browser backend built,
and repeated generation had no drift. The Go check found no vulnerabilities;
the production JavaScript audit also found none. All eight selected router tests
passed. This is source and generation evidence. The per-Gateway deployment and
rendered editor and terminal tests remain open.

The next CI job builds a rootless, read-only test image from the checked upstream
binary, then publishes it under `hypershell-stego-dashboard-ci`. It records the
registry digest and keeps the image archive. Package publication is limited to
this job; the source job has read-only permissions. A separate compiler pin in
`private-compiler-revision` selects the private application candidate.

The job inserts that real image digest into `private-service.yaml`, generates
and builds the browser backend, and checks the resulting two-container deployment.
It checks stable generation, secret separation, and absence of a direct
application Service port. The browser image reference used only for rendering
is an explicit example. This job does not deploy either container. Gateway
trust and mTLS files must be supplied in the application mount before deployment.
Published images are CI candidates, not qualified production releases.

The [registry check](../../acceptance/dashboard-private-registry-evidence.json)
passed. CI built and published the image, pulled it by digest, and compared its
configuration and filesystem identity with the build. The saved image contains
only the checked dashboard binary. Both source dependency checks passed. The
generated browser built with all eight upstream document routes, repeated
generation kept the same state, and the deployment checks passed. The live Pod,
editor, terminal, and Gateway lifecycle checks remain open.

The next source candidate connects page views to the generated
`gateway-console/out/browsertelemetry` package. The build copies those exact
files into the recorded upstream build tree and records their hashes. The
Hypershell hook supplies only fixed route names and page-view events. STEGO
supplies the trace, log, and metric runtime, its signal settings, authenticated
export, limits, and page-hide flush. The route test rejects resource names,
query values, and fragments in telemetry. Live delivery remains a required
part of the deployed dashboard workflow.

The next source candidate uses STEGO's generated `browser/sessionclient` for
upstream JSON requests. The bridge keeps the dashboard API paths and response
bodies. STEGO supplies the session, CSRF, cancellation, and size checks. The
production sign-in page calls the same generated client. It does not show local
development instructions. The source job checks the bridge and records the
exact generated client files with the source archive.

The cluster acceptance test now includes rendered workspace creation, reload
after namespace recovery, console and provider sign-out, and correlated
browser logs, traces, and metrics. The [session client source check](../../acceptance/dashboard-session-source-evidence.json)
passed all five gates and all 13 selected UI tests. The saved source tree matches
the application inputs and generated browser packages. Repeated capture of the
36 assets was identical. The [image check](../../acceptance/dashboard-session-image-evidence.json)
verified the published image against the checked binary. The captured bundle
and image are now selected by the Gateway console. Its current generated module
and the live workflow still require qualification. No live result is claimed
for these new checks.
