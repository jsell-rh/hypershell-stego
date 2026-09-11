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
and entry point. Generation, build, and live deployment results are recorded
below. Public ingress and credential rotation remain required. The earlier
rendered test used separate processes inside one bounded test Pod.

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
console deployment and credential rotation gates remained open at that stage.

The live deployment check uses the same Gateway assertions as the process test:

```sh
STEGO_TEST_CONTEXT=default/api-jshell-8u58-p3-openshiftapps-com:443/johnsell \
STEGO_TEST_BROWSER_DEPLOYMENT=1 scripts/check-service-deployment.sh
```

Run this command from a frozen checkout when source changes must continue.
The wrapper uses the named context and a new namespace. The Job builds the
pinned compiler and static API and console images, then publishes them to the
test namespace's registry. It checks both image configurations before use.
The published image references use digests. No local Go build or browser runs.

The API and console use their generated Deployments, Services, ServiceAccounts,
NetworkPolicies, TLS listeners, and projected file Secrets. PostgreSQL, the TLS
Kafka protocol fixture, the TLS collector, Chromium, and real Keycloak are test
dependencies. The API and console have separate databases and runtime roles.
The console role can access only the browser session table. Schema setup uses
the fixture owner before rollout. Runtime roles do not get schema ownership.

The test uses the cluster's Service host names as HTTPS origins. It checks the
live Pod's image, ServiceAccount, token mount setting, root filesystem setting,
and restart count. After creation, it replaces both Pods and requires new Pod
UIDs. The existing browser session must still work. The test retains the
rendered UI, owner-grant, filtered-list, denied-request, event, REST, gRPC,
collector-failure, renewal, and confirmed sign-out checks.

The wrapper saves results before namespace deletion. Its image publisher has
namespace-scoped registry credentials, which it removes after publication.
Generated application Pods do not receive those credentials. This test does not
prove public ingress or credential rotation. Those remain separate gates.

The first live check stopped before the application tests because npm tried to
create `/.npm` on a read-only root filesystem. Its result was 254. The wrapper
saved the failed Job and logs in `/tmp/stego-service-results.TtAcpYo1`, then
removed the namespace. The npm cache now uses the bounded work volume.

The second check built and published both images. The generated API passed its
live Pod restrictions, verified HTTPS readiness, and database TLS checks. Both
runtime roles passed the schema restriction check. The console role also passed
the domain-table privilege check. The run then failed because the console
renderer was invoked from the API module. Go rejected the nested module path.
The failure occurred before console startup, at 87.14 seconds. Its result and
Job records are in `/tmp/stego-service-results.MB5lnCMj`. The namespace was
removed. The test now runs each renderer from its own module directory.

The corrected build produced the same image digests as the second check:

- API: `sha256:d495bb6718007929dbdcd35c4db1a40f490477ed5bc6b33a6df2dc360e61d30e`.
- Console: `sha256:431728a557fe8da736674a09df1c3c9b3536888ae0df983e4b282cb977ec0a90`.

The third check passed startup and database TLS checks for both generated Pods.
The SDK created a Gateway, but its telemetry flush failed. The fixture had
passed the collector's local listen address to the separate Pods. The corrected
fixture uses the collector Service address and retains certificate verification.
This was an address error in the test setup. No runtime transport limit or TLS
check was relaxed. The failed run took 91.32 seconds. Its records are in
`/tmp/stego-service-results.lxieTQ4I`; the namespace was removed.

The fourth check passed SDK Gateway creation, access checks, event delivery,
and correlated browser telemetry. Chromium then rejected Keycloak's test
certificate with `ERR_CERT_AUTHORITY_INVALID`. The fixture supplied the CA's
key pin rather than the server certificate's key pin. The Go clients still
passed normal certificate verification. The run failed before rendered login
at 115.22 seconds. Its screenshot and records are in
`/tmp/stego-service-results.KTC0AqJe`; the namespace was removed.

The same browser login step failed in application CI run `34648296167`; the
other seven jobs passed. Commit `727fe70` supplies the server certificate pin
for both Docker and Kubernetes fixtures. It does not disable certificate checks
for other servers. External test providers can set
`STEGO_TEST_BROWSER_KEYCLOAK_CERT_FILE` to the server certificate path. When it
is absent, the fixture uses the supplied CA file for compatibility with the
existing self-signed server fixture.


The final check passed with compiler
`b3f690864dac20dcc766fb5aef6e4167aee8b654` on 2026-09-11. The application test took
125.63 seconds; its race-enabled package took 126.693 seconds. The contract and
input-manifest package passed in 1.062 seconds. The browser created and retrieved
a Gateway through the generated API and console Deployments. Its owner grant
and event were present. Owner access and denied requests passed through REST
and gRPC before and after replacement of both Pods. The existing browser
session remained valid.

Browser traces, logs, and metrics reached the TLS collector through the
registered console session. The trace included the backend and API parent
chain. The UI remained usable when the collector returned an error. All three
browser export routes returned the bounded failure response. Selected private
values were absent from process logs. Token renewal and confirmed console and
identity-provider sign-out also passed.

All 218 output, state, and Go dependency hashes match both generation passes,
the post-test check, and the local checkout. Both image digests match the prior
builds above. The Job reached `Complete`. Logs, hashes, image metadata, and the
rendered screenshot are saved in `/tmp/stego-service-results.hhsn9azV`.

This proves the rendered Gateway creation workflow through the generated
Deployments on jshell. It does not prove public ingress, certificate or session
key rotation, full console coverage, accessibility, capacity, or a Gateway
workload that has finished provisioning. Those gates remain open. CI for the
certificate-pin fix and the final application revision is tracked separately.
