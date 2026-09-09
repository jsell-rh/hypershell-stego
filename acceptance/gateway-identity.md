# Gateway identity workflow

`TestGatewayIdentityControllerWorkflow` runs the generated API, the identity
controller, the service-account provisioner, PostgreSQL, and Keycloak as separate
processes. It creates a Gateway through REST before the controller starts. It
then creates another Gateway through gRPC while the watch is open.

The gRPC creation occurs after the initial scan completes. It must reach the
controller through the live watch before the next scan.

The controller creates the Keycloak client and its trusted Gateway binding.
It creates the Gateway roles, token claim mappers, and role scopes before it
enables login. It publishes the resulting OIDC configuration through the generated
API client. A service-account request then uses this binding to obtain a real
credential. The test verifies the resulting token and refuses a different Gateway
audience. The fixture sets workload health before this request. The identity
controller does not deploy a workload or set health.

The test also checks API restart while the controller remains active. A separate
restart check changes a live Gateway and deletes another Gateway while the
controller is offline. A new controller repairs the live identity and removes
the deleted Gateway client. It uses the API state and provider inventory. It has
no retained process memory.

The control-plane `GetGatewayIdentityState` RPC includes retained deletion rows.
Only a verified subject in `HYPERSHELL_CONTROL_PLANE_SUBJECTS` can call it. Normal
owners and platform administrators have no implicit access. A missing row or a
denied request never permits provider deletion. Unit tests check these cases,
provider failures, API conflicts, and unchanged identity publication.

Run the workflow with the same PostgreSQL and Keycloak settings as the full
acceptance gate:

```sh
STEGO_REQUIRE_POSTGRES=1 STEGO_REQUIRE_KEYCLOAK=1 \
  go test -race ./acceptance -run '^TestGatewayIdentityControllerWorkflow$' -count=1
```

`STEGO_TEST_POSTGRES_DSN` must identify the disposable acceptance database server.
The test creates and removes its own database. It starts the pinned Keycloak image
through Docker. No running reference Hypershell service is required.

## Controller configuration

Run `go run ./cmd/gateway-identity-controller`. Supply these settings:

| Setting | Purpose |
| --- | --- |
| `HYPERSHELL_API_GRPC_ADDR` | API host and port |
| `HYPERSHELL_API_CA_FILE` | Trusted API certificate authority |
| `HYPERSHELL_API_TOKEN_FILE` | Private file with the controller bearer token |
| `HYPERSHELL_KEYCLOAK_URL` | HTTPS provider origin |
| `HYPERSHELL_KEYCLOAK_REALM` | Provider realm |
| `HYPERSHELL_KEYCLOAK_CLIENT_ID` | Provider administrator client |
| `HYPERSHELL_KEYCLOAK_SECRET_FILE` | Private administrator credential file |
| `HYPERSHELL_KEYCLOAK_CA_FILE` | Trusted provider certificate authority |

The API must allow the controller subject through its control-plane setting.
STEGO supplies TLS, token-file reads, message limits, call limits, deadlines, and
cancellation. Hypershell supplies the identity contract, reconciliation order,
provider ownership rules, and OIDC fields.

The controller has one worker and a queue of 1,024 resource IDs. It subscribes
before it reads current state. It drains the watch during that scan. An overflow
causes a new watch and scan. Each operation has a 20-second limit. A new scan
follows each 30-second session and a one-second delay. Each scan permits at most
10,000 Gateway rows and 10,000
provider clients. These limits are bounds, not measured production capacity.
The initial implementation uses one Gateway per API page to respect the generated
message limit. Large-scale throughput and scan fairness remain acceptance work.

Use one active identity controller. Coordination across multiple controller
processes remains open work. Failed configuration leaves a new client disabled.
A later scan retries the operation. A provider failure can delay cleanup. Retained
deleted Gateway rows are required for cleanup after an offline deletion.

## Reference differences and remaining scope

The client ID is `hs-gateway-{id}`. It uses the immutable Gateway ID, so a rename
does not change the audience. The reference uses a name and ID. This test bed does
not migrate existing reference clients or adopt clients without trusted binding
attributes.

The client permits browser login with PKCE S256 and device login. It disables
password, implicit, client-credential, and CIBA grants. It has no default or
optional client scopes. Its own role scope and claim mappers supply the Gateway
subject, audience, and roles. The reference enables the password grant. The variant follows
[RFC 9700 section 2.4](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.4).

This workflow proves identity provisioning and service-account token use. It does
not prove device login completion,
Kubernetes deployment, Gateway workload health, or the other resource controllers.
It does not establish full reference compatibility or production readiness.

Browser login and Gateway user-role changes now have
[a separate application check](gateway-user-login.md). It covers subject identity,
role union, removal, and restart. It also states the limits of token revocation
and provider synchronization.
