# Protected dashboard login

The [live check at 906f75c](https://github.com/jsell-rh/hypershell-stego/actions/runs/35155975877)
passed verified HTTPS and the protected-document redirect probe. The browser
reached the identity provider and submitted the test credentials. Its next
saved `/workspaces` response was HTTP 200 with STEGO's sign-in fallback. The
upstream Workspaces page did not appear. The workflow failed after 279.79
seconds. The later restart and recovery checks did not run.

The test did not record browser cookie exclusion reasons. The fallback is
consistent with a Strict session cookie absent from the identity provider's
redirect chain, but that exact browser decision remains an inference. Both
Gateways, dashboards, and generated browser backends were ready with no
container restarts. Independent cleanup passed at
`2026-09-16T22:21:40.225959Z`; the test lease was empty.

STEGO revision `883ca13a1f147f1c6778f628ebcd7e2f3d8bb053` includes the common
[login completion change](https://github.com/jsell-rh/stego/blob/883ca13a1f147f1c6778f628ebcd7e2f3d8bb053/specs/browser-login-document.md).
For protected applications, a valid callback returns a small HTML document.
That document opens the validated application path. The session cookie remains
Secure, HttpOnly, host-only, and SameSite=Strict. The management console retains
its HTTP 303 callback. No Hypershell login mechanism was added.

Both compiler pins and all three common registry pins select that revision.
All three generated state files matched on repeated generation. The Gateway
console input check passed. The HTTP fixture checks the protected callback's
document, return path, privacy headers, and retained cookie restrictions. The
rendered browser check still requires the real upstream UI without a test-side
reload or sign-in workaround.

Common CI, the published module check, and the deployed application check must
pass before this candidate is qualified. Repeated generation and HTTP fixture
checks do not prove browser SameSite behavior.
