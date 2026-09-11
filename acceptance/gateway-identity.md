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

The `gateway-identity` worker declaration makes STEGO generate the main,
Containerfile, health probes, and deployment resources. Hypershell supplies
provider setup through `internal/gatewayidentityapp.Run` and retains domain rules.
Run `go run ./out/deploy/workers/gateway-identity`. Supply these settings:

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
The API must also give that subject a `Gateway` / `configure.identity` grant
with an empty target in `HYPERSHELL_CONTROLLER_WRITE_GRANTS`. Cleanup requires
its separate `cleanup.identity` grant in `HYPERSHELL_CLEANUP_GRANTS`. See the
[controller write permissions](controller-write-permissions.md).
STEGO supplies TLS, token-file reads, message limits, call limits, deadlines, and
cancellation. Hypershell supplies the identity contract, reconciliation order,
provider ownership rules, and OIDC fields.

The controller uses STEGO's generated `RunKeyedWatch` with four workers and a
queue of 1024 resource keys. Each Gateway has at most one active action within a
Run call. Failed keys retry with delays from one to ten seconds. Repeated events
cannot bypass that delay. Other Gateways can progress while one provider call
waits. The controller subscribes before recovery scans and provider actions.
Scans and watch delivery wait for queue capacity and stop on cancellation.

Each action has a 20-second limit. A new recovery scan starts 30 seconds after
the previous scan finishes. A failed watch cancels and joins its callbacks before
reconnect. Reconnect waits one second and starts another scan. Recovery permits
10000 API pages of 100 IDs. The separate provider inventory permits fewer than
10000 clients. These bounds are not measured production capacity.

A short lock protects the existing per-Gateway user-scan cursors. It is not held
during API or provider calls. The cursor preserves progress after an action time
limit. Each provider write still reads the current user grant first. Completed
scans and deleted Gateways remove their cursors. These cursors remain in process
memory; the queue limit does not provide a complete process-memory bound.
Durable cursor state and inventory limits remain separate work.

Use one active identity controller for each ownership scope. Coordination across
processes remains open. Failed configuration leaves a new client disabled.
Retained deleted Gateway rows are required for cleanup after an offline deletion.
A queue filled by persistent failures can still prevent new keys from entering.
The durable retry-storage choice remains pending. See the
[scheduling evidence](gateway-scheduling.md).

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
not prove device login completion, Gateway workload health, or the other
resource controllers. The [cluster check](kubernetes-service.md) covers the
generated API and identity worker Deployments.
It does not establish full reference compatibility or production readiness.

Browser login and Gateway user-role changes now have
[a separate application check](gateway-user-login.md). It covers subject identity,
role union, removal, and restart. It also states the limits of token revocation
and provider synchronization.

[Durable identity cleanup](gateway-identity-cleanup.md) now records confirmed
client absence through STEGO and checks for late effects after completion.
