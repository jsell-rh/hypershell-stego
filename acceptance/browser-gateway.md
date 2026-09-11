# Browser Gateway protocol check

The user selected a separate Go browser backend with compatible browser HTTP
contracts. The console uses STEGO's browser-service archetype in its own Go
module. Hypershell declares assets, routes, the API prefix, and the role claim.
STEGO supplies login, encrypted database sessions, token renewal, logout,
request limits, CSRF checks, the API proxy, health checks, and telemetry setup.
No application copy of this common runtime is required.

`TestGeneratedBrowserGatewayWorkflow` runs real Keycloak and PostgreSQL with
separate generated API and console processes. It checks these operations:

- Login with authorization code, PKCE, and a confidential console client.
- Create a Gateway with its owner grant, required IDs, and API response shape.
- Check owner access, filtered lists, denied reads, and denied creation.
- Read the Gateway through REST and gRPC with owner and non-owner identities.
- Deliver the committed event through the generated API runtime.
- Stop and restart the console process without loss of its stored session.
- Renew an expiring token and confirm that the encrypted session changes.
- Log out and deny further API requests with the old browser session.

The test uses different loopback IP addresses for the console and other
services. It checks secure cookie attributes and confirms that console cookies
do not reach the API or identity provider. A Go HTTP client checks the protocol;
it does not prove a browser's SameSite enforcement or render the page.

The bounded jshell check passed under race detection in 21.98 seconds
(23.027 seconds for the acceptance package). All 23 console output, state, and
dependency hashes matched before and after the test. The frozen sources and
results are in `/tmp/stego-browser-workflow-pwsizhdc` on the test workstation.
The first attempt found a missing tracing binding in the new common archetype.
The next attempt found a nil header map in the test client. Both were corrected
before the final fresh check.

Run `scripts/generate.sh` to generate the API and console with the same pinned
compiler. CI runs this test through `scripts/check-gateway.sh`. It also checks
the nested console module for known dependency vulnerabilities.

The browser page is a scaffold. A complete Gateway UI, browser telemetry export
checks, browser cookie enforcement, production console deployment, session key
rotation, and measured capacity remain open. This result does not close the
complete Hypershell or STEGO enterprise goals. No Playwright or workstation
performance test was used.

The published compiler `a6e7656` generated both services twice. All 152 output,
state, and dependency hashes matched both passes and the checkout. The root
input-manifest race test passed in 1.060 seconds. The console runtime files
match the files used by the protocol test; only the compiler state record
changed during adoption. Full CI for this revision remains a separate check.
The cluster Job completed. Its namespace was removed, and the cluster API
confirmed removal.
