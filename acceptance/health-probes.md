Hypershell now uses STEGO's generated `health-check` component at version 1.0.0.
The compiler pin is `edc5bca696f1aeb5507eac458a758d17fc911412`. The service
selects `database: true`; it adds no application health loop or probe handler.

`GET /livez` returns 200 while the HTTP process responds. `GET /readyz` returns
503 until the first successful SQL connectivity check. It also returns 503
on failed or stale evidence and on cancellation. Healthy probes contain `ok`
and unavailable probes contain `unavailable`, each followed by a newline.
Both prevent caching. The routes are public but return no database or provider
error text. Application routes retain their access checks.

STEGO samples the shared SQL pool in one background loop. A cycle has a 500 ms
deadline, followed by a one-second wait. Successful evidence expires two seconds
after the start of its cycle. Probe requests only read that evidence. A slow
database therefore cannot make probe traffic create more database queries.
See the [compiler contract](https://github.com/jsell-rh/stego/blob/edc5bca696f1aeb5507eac458a758d17fc911412/specs/health-probes.md).

`TestGatewayAPIHealthAcrossDatabaseDelayAndRestart` uses PostgreSQL and the
actual generated API process. A local test proxy delays database traffic without
closing the event subscription. The test observes 503 readiness with 200
liveness, restores traffic, and verifies readiness and authorized Gateway
retrieval. An unauthenticated Gateway read still fails. It then restarts the
API and retrieves the same Gateway. The proxy is test code only.

The original test failed because `/livez` was absent. Adding the component
exposed a compiler assembly defect that left a route pointing to a renamed
handler. STEGO now passes the wrapped handler directly to the top-level mux.
A separate generated service reproduces the name conflict and checks both
permitted and denied requests.

With the pinned compiler, the health workflow passed under race detection in
6.15 seconds. Five selected acceptance workflows passed in 22.986 seconds with
PostgreSQL required. They cover health recovery, REST and gRPC Gateway access,
event delivery across restart, and shutdown after an event-source failure.
All internal and contract race tests passed. Static checks passed. The full
compiler race suite passed with PostgreSQL required. This change did not repeat
the full application suite, Keycloak, Kubernetes, or VM gates locally. Remote
CI still has to establish those results for this revision.

Generation added `out/health/health.go`. Among the previous 90 output, state,
and dependency files, only `out/main.go`, `.stego/state.yaml`, and the CLI
compiler build record changed.
Repeated pinned generation preserved all 91 output, state, and dependency hashes.

Database readiness proves connectivity only. It does not certify the schema,
write permission, provider state, gRPC readiness, or event delivery. Deployment
probe configuration and controller and gRPC health endpoints remain open.
