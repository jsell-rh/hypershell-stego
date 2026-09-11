# Console deployment

The console uses STEGO's `kubernetes-service` component. Its Deployment,
ServiceAccount, HTTPS Service, NetworkPolicy, and container build are generated.
The API and console remain separate workloads with separate runtime and file
Secrets. No common deployment template is copied into domain code.

Build from the console module directory. The generated output contains the UI
assets, so this build does not need Node.js or the UI source tree:

```sh
docker build --file console/out/deploy/Containerfile --tag hypershell-console:check console
```

Publish the image, then render with its full digest from the console directory:

```sh
go run ./out/deploy/render --image registry.example/team/console@sha256:FULL_DIGEST --namespace services --fs-group NAMESPACE_GROUP
```

Replace `FULL_DIGEST` and `NAMESPACE_GROUP` with the published image digest and
the namespace's file group. The renderer does not apply resources. The image
has no shell, uses a non-root user, and contains system CA roots. Its Pod drops
all capabilities, has a read-only root filesystem, and does not mount a service
account token. It has the same fixed resource limits as the API.

Provide `hypershell-console-runtime` for environment settings and
`hypershell-console-files` for files. Mounted files use mode `0440` at
`/var/run/stego`. Supply these settings through the runtime Secret:

| Setting | Value |
| --- | --- |
| `DATABASE_URL_FILE` | Path to a connection string for the console session database; use `sslmode=verify-full` and the database CA file |
| `STEGO_BROWSER_ORIGIN` | Exact public HTTPS origin for the console |
| `STEGO_BROWSER_API_URL` | API HTTPS origin on a different host |
| `STEGO_BROWSER_API_CA_FILE` | Path to the API CA file, if it uses a private CA |
| `STEGO_BROWSER_ISSUER` | Exact HTTPS OIDC issuer on a different host |
| `STEGO_BROWSER_ISSUER_CA_FILE` | Path to the issuer CA file, if it uses a private CA |
| `STEGO_BROWSER_CLIENT_ID` | Confidential console client ID |
| `STEGO_BROWSER_CLIENT_SECRET_FILE` | Path to the client secret |
| `STEGO_BROWSER_SESSION_KEY_FILE` | Path to a base64-encoded 32-byte random session key |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | HTTPS origin for the gRPC OTLP collector |
| `OTEL_EXPORTER_OTLP_CERTIFICATE` | Path to the collector CA file, if it uses a private CA |
| `OTEL_TRACES_SAMPLER_ARG` | Trace sample ratio from 0 through 1 |
| `OTEL_LOGS_EXPORTER` | `otlp` to export logs |
| `OTEL_METRICS_EXPORTER` | `otlp` to export metrics |

Supply `tls.crt` and `tls.key` in the file Secret. The Deployment fixes the HTTPS
port to 8443 and disables the database plaintext exception. Those settings take
precedence over the environment Secret. Apply `console/out/browser/schema.sql`
to a separate session database before rollout. Use a database role limited to
that database. Do not put secret values in source control or rendered resources.

The console uses the server's telemetry settings for browser traces, logs, and
metrics. Its public HTML contains only signal flags and the sample ratio. The
collector URL and trust files remain on the server. Browser exports require an
authenticated session and the common request bounds. API and console telemetry
can use the same collector. Their service names remain distinct.

The checked-in network peers select the bounded test fixtures. The console can
reach the API on 8443, PostgreSQL on 5432, Keycloak on 8443, and the collector on
19093. Only the test client can connect to the console. The API permits console
traffic on 8443. Production deployments must declare the actual peers and public
ingress path, then regenerate. External endpoints require explicit render-time
IP and port bindings. Changing a URL does not change network policy.

The console, API, and identity provider need different cookie hosts. Register
the exact callback and logout redirect URIs from [the console guide](../console/README.md).
A public ingress must preserve the configured HTTPS origin and verify backend
TLS. This change does not install an ingress controller or create certificates.

The session encryption key is read at startup. All concurrent console instances
must use the same key. Replacement of that key needs a coordinated session
migration or session invalidation; a rolling update with unrelated keys is not
a supported rotation procedure. Production credential rotation remains an open
gate. A changed Secret alone does not prove runtime reload.

The image CI job builds the generated console Containerfile and checks its user
and entry point. Generation and build results are recorded below. A live check
of the console Deployment, projected credentials, public ingress, and Pod
replacement remains required. The prior rendered test used separate processes
inside a bounded test Pod; it does not prove these deployment properties.

Compiler `b3f690864dac20dcc766fb5aef6e4167aee8b654` passed the bounded jshell
check on 2026-09-11. The console builds with its embedded UI, including a static
build with CGO disabled. `TestConsoleDeploymentIsolation` and
`TestGeneratedProjectInputManifest` passed under race detection in 1.055 seconds.
All 218 generated, state, and Go dependency hashes match two generation passes
from the published compiler and the checkout. The cluster accepted all four
rendered resources in a server dry run. No application Pod was created by that
check. Full compiler CI passed. The new application image CI is a separate check.

The Job reached `Complete`. Its logs, result markers, hashes, and rendered
resources were saved under `/tmp/stego-console-deploy-2hxbs5ot` on the test
workstation. Namespace deletion was requested after collection. The live
console deployment and credential rotation gates remain open.
