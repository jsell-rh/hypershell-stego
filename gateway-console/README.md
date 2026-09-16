# Gateway console backend

This is the separate Go browser backend for the upstream OpenShell dashboard.
STEGO generates the backend, browser telemetry package, session schema, image
build, and importable Kubernetes deployment package. The dashboard process
listens on `127.0.0.1:8000` in the same Pod. Only the browser HTTPS port is
exposed by the generated Service. The containers have separate credential
mounts. Browser JavaScript receives no OAuth token.

[upstream.json](upstream.json) pins the checked upstream build, container image,
and captured assets. The [build inputs](../components/gateway-dashboard) retain
the dependency changes and upstream license. [.stego/config.yaml](.stego/config.yaml)
combines the pinned common STEGO registry with the local application archetype.
The compiler and common registry use the same full commit SHA. The local
archetype adds browser telemetry; it contains no copied component metadata or
new runtime implementation.

Run `scripts/generate-gateway-console.sh` from the repository to regenerate.
Use `--check` to reject differences from committed output. The script retains
a clean compiler checkout and generation records in the operator's state
directory. `STEGO_GENERATION_ROOT` can select another absolute record directory.
The repository generation check includes this module.

Deployment requires a separate session database and login, the generated
session schema, a confidential OAuth client, session keys, verified HTTPS
certificates, Gateway mTLS files, and approved network destinations. The
controller must supply those inputs. It must use the generated deployment
package and preserve resource ownership. The dashboard application must use
`AUTH_DISABLED=false`, `LOGOUT_URL=/auth/logout`, and its verified Gateway
connection. It must not receive the browser session keys or database login.

This module is an application integration candidate. The Gateway controller
does not yet deploy it. The captured UI imports the generated browser telemetry package.
The [source evidence](../acceptance/dashboard-telemetry-evidence.json) includes
nine passing route tests and checks fixed route labels for private data. Live authentication, editor and terminal behavior,
restart, address publication, and complete deletion remain required.

The identity worker can now create the dashboard's confidential OAuth client.
Set `HYPERSHELL_GATEWAY_CONSOLE_DOMAINS` to a JSON object that maps each managed
cluster ID to its operator-controlled DNS domain. An empty setting disables
new console clients. A nonempty setting must include every reconciled cluster.
The host uses the complete Gateway ID and does not change on rename.

The console client uses the native Gateway client's roles and audience. It
creates no second set of grants. STEGO controls client repair and deletion.
The API stores its encrypted recovery record in a separate fixed scope. It
uses the same assigned identity controller and cleanup grants, with resource
and record version checks. Cleanup attempts both client closures and retains any error for retry. A console
failure must not leave native closure open. This change does not yet supply
credentials to a dashboard Pod. The deployed application workflow still
requires that step.

The [identity evidence](../acceptance/console-identity-evidence.json) records 21
passing checks against real Keycloak and PostgreSQL. It includes PKCE console
login, stored owner and viewer grants, grant removal during a console placement
fault, and restart. The controller applies grants before client repair. It
retries from current state if a grant observation changes the resource revision.
This evidence does not prove a deployed dashboard workflow.

The trusted provisioner exposes an internal console credential RPC. Configure
`HYPERSHELL_CONSOLE_CREDENTIAL_GRANTS` with exact issuer, subject, `Gateway`
resource, `read.console-credential` operation, and managed cluster ID as target.
No account provisioning subject receives this grant by default. The request
must match the current Gateway ID, cluster, and resource revision. The service
checks that observation before and after the protected credential read.

The provisioner's API identity also needs a separate `HYPERSHELL_PROVIDER_STATE_GRANTS`
entry for `Gateway`, operation `read.console-client`, and an empty target. This
grant permits only console journal reads. It does not permit native journal
reads, journal writes, or cleanup. The provisioner uses the instance's identity
state keys and console domain policy. Gateway Pods receive no administrator
credential or state key through this interface.

The [database workflow evidence](../acceptance/console-database-workflow-evidence.json)
records real PostgreSQL creation, limited console access, session state across
restart, and independent cleanup of Gateway and console databases. It also
records all 51 required API tests and verified cluster cleanup. This is not a
rendered dashboard result.
