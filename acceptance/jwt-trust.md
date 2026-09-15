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

Browser run `34967271404` is active; full CI `34967271365` is also active.
Their results remain required. Do not restart an active run because an
observation times out or a result is not yet available.
