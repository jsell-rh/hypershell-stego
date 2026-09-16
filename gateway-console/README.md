# Gateway console backend

This is the separate Go browser backend for the upstream OpenShell dashboard.
STEGO generates the backend, browser telemetry package, session schema, image
build, and importable Kubernetes deployment package. The dashboard process
listens on `127.0.0.1:8000` in the same Pod. Only the browser HTTPS port is
exposed by the generated Service. The containers have separate credential
mounts. Browser JavaScript receives no OAuth token.

[upstream.json](upstream.json) pins the checked upstream build, container image,
and captured assets. The [build inputs](../components/gateway-dashboard) retain
the dependency changes and upstream license. The local registry contains exact
common component metadata from [.stego/compiler-revision](.stego/compiler-revision).
Its browser archetype adds the common browser telemetry component. It contains
no new runtime implementation.

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
does not yet deploy it. The captured UI does not yet import the generated
browser telemetry package. Live authentication, editor and terminal behavior,
restart, address publication, and complete deletion remain required.
