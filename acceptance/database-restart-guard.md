# Database restart fixture guard

[Live run 35493398031](https://github.com/jsell-rh/hypershell-stego/actions/runs/35493398031)
failed at source `fdb06e20a192f2d5e16d98735528a09565e77b47`. All ten preliminary
tests passed. The rendered workflow failed after 355.37 seconds when its
database restart guard could not verify the fixture Pod. The restart command
was not reached. The final trace check and normal cleanup measurement were
not reached. This run does not qualify the complete application workflow.

The exact cause is unknown. One message covered a request error, unexpected
HTTP status, an invalid object, missing identity, and incorrect labels. The
saved Job template has the expected labels. Earlier reads of the same Pod
succeeded. Those observations do not identify the failed request condition.
Telemetry export errors in later cleanup logs do not establish that cause.
See the [failure and independent cleanup record](database-restart-failure-evidence.json).

The guard now reports a fixed category and HTTP status. It does not include
provider error text, object contents, credentials, or request paths. It retains
the five-second read deadline, exact fixture labels, required Pod identity,
PostgreSQL status check, and single request. No retry or longer deadline was
added. The later restart check still requires the same Pod UID, an increased
container restart count, and readiness.

Focused tests cover allowed status, missing and incorrect identity, malformed
objects, denied requests, cancellation, deadline expiry, and private errors
that panic if formatted. They require one request with the original context
and exact path. Hosted checks and a fresh complete workflow remain required.
The compiler and generated runtime are unchanged.
