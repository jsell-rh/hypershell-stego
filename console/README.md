# Hypershell console service

This service uses STEGO's common browser session backend. Hypershell supplies
the API prefix, identity role claim, public routes, and asset inputs. Login,
session storage, token renewal, logout, and the API proxy are generated.
The browser page is a scaffold. A complete Gateway user interface is still
required.

Run `scripts/generate.sh` from the repository root to generate both services
with the same pinned compiler. The console has its own Go module. Build its
process with `cd console && go build ./out` in CI or the test cluster.

Apply `out/browser/schema.sql` to the console session database before startup.
Use the generated backend's documented environment settings for database TLS,
service TLS, API trust, OIDC, the client secret file, and the session key file.
Give the console a host name that differs from the API and Keycloak hosts.
Different ports on one host do not isolate browser cookies. Register the
console's exact HTTPS origin plus `/auth/callback` with Keycloak.
Also register the origin plus `/auth/logout` as a post-logout redirect URI.
The declaration selects identity-provider sign-out. GET `/auth/logout` shows a
confirmation page. Its form removes the console session, revokes its token,
and sends the browser to Keycloak to finish sign-out. A cancelled console form
keeps the session active. No ID token is placed in browser HTML or URLs.
The confidential client needs authorization code flow and S256 PKCE. Its
access tokens need the `hypershell` audience. Its identity token audience must
identify the console client. The console uses `resource_access.hypershell.roles`
for the browser's role hints. The API still enforces access from stored grants.

The acceptance test uses a separate API process and console process with real
Keycloak. It checks the browser HTTP protocol with a Go HTTP client. It does
not claim browser enforcement of cookie attributes or a rendered UI check.
No Playwright is used.
