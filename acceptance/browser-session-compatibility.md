# Browser session compatibility

The reference console uses a GET sign-out link and a fixed 401 response to
restart login. The first generated backend returned 405 for that link and a
generic 401 body. These responses did not meet the host contracts.

STEGO browser-backend 1.1.0 adds a GET confirmation page and the response fields
`error: reauth_required`, `login_url: /auth/login`, and `statusCode: 401`.
Invalid local sessions and API authentication rejection use this response.
Permission denial does not remove a valid session. Storage failure remains 503.

GET sign-out leaves the session active. The form requires the session CSRF
value and the exact console Origin. The Hypershell declaration selects
`logout_scope: identity_provider`. After confirmation, STEGO removes the local
session and revokes its token before it sends the browser to the discovered
provider logout endpoint. The endpoint must use HTTPS and a separate cookie
host. Its return address is the fixed console origin plus `/auth/logout`.
Keycloak must register this exact address as a post-logout redirect URI.

The request contains the client ID and return address. It contains no ID token
hint. Keycloak can ask for sign-out confirmation. If token revocation has already
ended its session, it can return directly to the console. The standalone POST interface
with `X-CSRF-Token` retains its 204 local sign-out contract. It does not navigate
the browser or claim to end the provider session. If token revocation fails,
STEGO reports an error and keeps the local session removed.

The extended Gateway test checks access after opening the confirmation page,
submits the console form, follows provider sign-out, checks denied Gateway access, checks
the 401 response, and requires a password for the next login. The common runtime
tests also reject invalid forms and unsafe provider endpoint addresses.
The corrected cluster workflow passed in 15.41 seconds (16.459 seconds for the
package). The common runtime race checks passed in 8.978 seconds. The records
are in `/tmp/stego-browser-logout-zw9kt6cp`. A fresh check with the published
compiler is described below. These are HTTP protocol checks. They do not
prove rendered browser behavior or complete migration of the reference UI.

The reference UI still uses a trex-generated TypeScript SDK. Its mutation
transport does not yet send the new CSRF header. Its asset build and browser
telemetry also need integration with STEGO. These are required migration work,
not covered by the sign-out result. The current console page remains a scaffold.

The first common check rejected the existing empty JSON logout request. The
parser was corrected. The first application check expected readiness before the
first health sample. It now waits within a fixed five-second deadline. The next
check assumed that Keycloak must show a confirmation form. Token revocation
had already ended that session, so Keycloak returned directly to the console.
The test now accepts either provider response and still requires a password
for the next login. Failed results are retained. The original Job has failed
status; its later workflow result does not replace that status. Its namespace
was removed before the fresh check started.

The fresh check used published compiler `e7febe3`. The complete Gateway browser
workflow passed in 23.15 seconds (24.198 seconds for the package). The root
input-manifest race test passed in 1.059 seconds. All 152 generated, state, and
dependency hashes matched both generation passes and the checkout. The tested
application source also matches the checkout. The Job completed. Its source and
results are in `/tmp/stego-browser-logout-pin-n9t00fim`. Full compiler CI passed
for `e7febe3`; full application CI remains a separate check.
Both test namespaces were removed. The cluster API confirmed removal.

The [TypeScript client check](browser-typescript.md) now tests the generated
CSRF transport with real Gateway requests. Integration with the reference UI
and browser telemetry remains open.

The user confirmed on 2026-09-11 that console sign-out must end both the console
and identity-provider sessions, with a confirmation page. This is now an
explicit requirement. The existing `identity_provider` setting and Keycloak
acceptance test implement this choice. Opening the confirmation page must not
end either session.
