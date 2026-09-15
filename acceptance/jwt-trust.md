Hypershell now pins compiler `a355306`, whose
[full CI passed](https://github.com/jsell-rh/stego/actions/runs/34966748920).
That compiler also closes the separate SSO example's issuer, audience, and
expiry defect through the shared JWT runtime. Hypershell uses `jwt-auth`.

The generated API adopts the common key decoder and optional bounded JWKS key
source. Its existing static-key authentication mode remains in use. The compiler
checks include signed tokens, verified TLS, key rotation, provider failure and
recovery, input limits, and shutdown cancellation. Both independent example
services passed vulnerability checks, race tests, builds, and regeneration.

API and console generation, dependency resolution, repeated apply, and drift
checks passed with the pinned compiler. The complete Hypershell API, browser,
and full CI gates are required for this application revision. Earlier results
apply to their recorded sources; a compiler pass does not replace these gates.

See [the shared key source contract](https://github.com/jsell-rh/stego/blob/main/specs/jwt-key-source.md)
and [SSO audit](https://github.com/jsell-rh/stego/blob/main/specs/sso-auth-audit.md).

The [complete API evidence](shared-jwt-api-evidence.json) records a pass for
Hypershell `752d92e` in run `34967271359`. All 31 required tests passed in
166.638 seconds. Verification matched 911 source files, 231 generated files,
and four generation records. The retired database request test, worker startup
signals, and pool wait, restart, and recovery checks passed. Automatic cleanup
and independent reads found no test resources.

The [complete browser evidence](shared-jwt-browser-evidence.json) records a pass
in run `34967271404` at the same source. The workflow took 377.04 seconds. It
verified 911 source files, 232 generated files, and three matching generation
records. It passed real Keycloak login, Gateway creation, access checks, event
delivery, worker and API restart, namespace recovery, SQL isolation and cleanup
recovery, credential encryption, account deletion, session renewal, and logout.
Worker and SQL telemetry checks passed. Automatic cleanup and independent reads
found no test resources, and the shared Lease was free.

The reviewed Gateway page shows Healthy and no accounts after deletion. Its
connection panel still shows loading placeholders. Public Gateway connectivity
remains unverified. Full CI `34967271365` is still active; its final result
remains required.
