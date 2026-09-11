# Hypershell console service

This service uses STEGO's common browser session backend. Hypershell supplies
the API prefix, identity role claim, public routes, and asset inputs. Login,
session storage, token renewal, logout, and the API proxy are generated.
The backend serves the built React console from `ui/build.zip`. The rendered
Gateway workflow is checked with Chromium through WebDriver.

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

The acceptance test uses separate API and console processes with real Keycloak.
It checks the browser protocol and the rendered Gateway workflow. The checks
include access rules, telemetry, API and console restart, and confirmed sign-out.
No Playwright is used. See [the UI test record](../acceptance/web-console-port.md).

The browser archetype also generates a restricted container and Kubernetes
resources. See [console deployment](../acceptance/console-deployment.md) for
build context, secrets, trust, network peers, and the remaining deployment gate.
