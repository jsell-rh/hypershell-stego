The service-account workflow now uses a real Keycloak provider. The application
creates accounts through REST. A separate provisioner process serves the pinned
internal RPC contract through STEGO's generated TLS runtime. Hypershell owns
client settings, role mappings, audience mappings, ownership checks, and lifecycle
operations. STEGO owns HTTPS limits, JWT signature verification, and RPC transport.

Run `go run ./cmd/provisioner` with these settings:

| Setting | Purpose |
| --- | --- |
| `HYPERSHELL_KEYCLOAK_URL` | Canonical HTTPS server URL, with an optional base path |
| `HYPERSHELL_KEYCLOAK_REALM` | Realm name |
| `HYPERSHELL_KEYCLOAK_CLIENT_ID` | Administrator service-account client |
| `HYPERSHELL_KEYCLOAK_SECRET_FILE` | Private file with that client's secret |
| `HYPERSHELL_KEYCLOAK_CA_FILE` | Trusted Keycloak CA certificates |
| `HYPERSHELL_PROVISIONER_SUBJECTS` | Required JSON array of allowed caller subjects |
| `STEGO_AUTH_ISSUER`, `STEGO_AUTH_AUDIENCE`, `STEGO_AUTH_PUBLIC_KEY_FILE` | Trust settings for internal caller tokens |
| `STEGO_GRPC_ADDR`, `STEGO_GRPC_TLS_CERT`, `STEGO_GRPC_TLS_KEY` | Internal listener and TLS identity |

Point the API process at this listener with the service-account provider settings
in [the lifecycle instructions](service-accounts.md). Keep the Keycloak administrator
secret in the provisioner process. The administrator account must have permission
to manage clients, users, and their role mappings within the configured realm.
Do not give this credential to API callers.

New clients start disabled. The provider removes unrelated role mappings, assigns
the selected Gateway roles, installs restricted audience and role mappers, and
enables the client. Before returning the secret, it obtains an access token and
verifies its signature against keys fetched from the configured HTTPS issuer.
It checks issuer, subject, authorized party, exact audience, exact roles, expiry,
and the requested token lifetime. It rejects refresh tokens.

The HTTPS client permits one configured service only. It requires TLS 1.3 and an
explicit CA. It rejects redirects and does not use environment proxy settings.
It bounds request bodies at 1 MiB, response bodies at 4 MiB, headers at 32 KiB,
and concurrent requests at 16 per client. Each HTTP request has a five-second
deadline. The generated RPC client also bounds the complete API-to-provider call
at five seconds. Errors omit provider bodies and credentials.

The adapter reads the administrator secret again when it refreshes its token.
Concurrent refresh waits honor cancellation. Failed creation attempts cleanup
with a separate five-second context. Stable resource IDs permit later cleanup
if the response to client creation is lost. Disabled and deleted clients require
matching Gateway and account ownership metadata. A missing ownership ID fails.

`TestServiceAccountsWithRealKeycloak` uses a pinned Keycloak 26.7.3 container,
PostgreSQL, and separate API and provisioner processes. It proves:

- REST creation returns a credential that obtains a signed token.
- The token has the selected Gateway audience and admin roles, with no refresh token.
- Another Gateway audience and an unlisted internal caller are denied.
- Later API reads and stored account state contain no client secret.
- Repair removes injected client-setting drift.
- A creator downgrade reduces the roles in new tokens to `openshell-user`.
- A revocation committed while the provider is stopped returns HTTP 202.
- Restart recovery stops new token issuance, and deletion removes the client.

Terminal revocation now removes the Keycloak client. The account row, its
`revoked` or `expired` state, and its audit history remain in PostgreSQL. A later
delete request removes the visible account record. This differs from the
reference, which retains a disabled Keycloak client after revocation. Deletion
is the current design choice after the user was asked about identity retention.

`TestRevocationSurvivesDelayedEnableAfterDatabaseLoss` exposed a failure in the
disabled-client policy. The test holds an accepted enable request at a TLS
proxy. It terminates the PostgreSQL connection that holds the Gateway lock,
then revokes through REST. It stops the API process before releasing the old
request, so recovery cannot hide a temporary reactivation. Before the fix, the
delayed update returned HTTP 204 and the revoked credential obtained a token.
With client deletion, the same update returns HTTP 404 and issuance stays denied.

The test also checks retained metadata, restoration of the owner grant, process
restart, repeated revocation, and later deletion with audit retention. The
application uses the existing generated Delete RPC for terminal revocation;
the protobuf contract and the provider's separate Disable operation are unchanged.
The domain provider interface now names this terminal operation `Revoke`.

Set `STEGO_REQUIRE_KEYCLOAK=1` to require this test. Docker must be available.
`scripts/check-gateway.sh` and CI require it. The container is isolated, uses
test credentials, and exposes only its TLS port on the loopback interface.
It uses the development profile and an embedded database. These are test setup
choices, not production deployment instructions. The first successful local run
completed in 33.14 seconds with Go 1.26.8 and PostgreSQL 18.6.

Remaining work includes automatic scans for provider drift and orphan clients,
production administrator permissions, recovery capacity, and other external
failure cases, including late creation after cleanup. The delayed-update test
does not prove all provider failure orderings. Revocation
stops new token issuance. Already issued access tokens can remain valid until
their five-minute expiry. Internal caller signing-key rotation still requires
a process restart. Provider token verification fetches current signing keys for
each creation; it does not cache them.

The adapter derives from the Apache-2.0 reference at commit
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`, under
`components/control-plane/internal/serviceaccountkeycloak` and
`components/control-plane/internal/serviceaccountprovisioner`. The variant replaces
the reference HTTP transport and unverified token inspection. See Keycloak's
[container guide](https://www.keycloak.org/server/containers),
[TLS guide](https://www.keycloak.org/server/enabletls), and
[service-account administration guide](https://www.keycloak.org/docs/latest/server_admin/).
