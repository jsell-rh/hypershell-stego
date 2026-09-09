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

Each Gateway client must have these Keycloak attributes:

| Attribute | Required value |
| --- | --- |
| `hypershell.gateway` | `true` |
| `hypershell.gateway-id` | The immutable Gateway ID |

The trusted control plane must set this binding when it creates the client.
The provider checks the client ID and binding before it reads roles, creates an
account, or repairs account settings. OIDC fields from an API caller cannot
establish the binding. Missing or incorrect attributes cause refusal. Existing
clients need a trusted migration; the provider does not adopt them from caller
input. The acceptance fixture uses its administrator to set the binding. The
control-plane port and migration procedure remain open.

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

`TestLateKeycloakCreationIsRemovedAfterCleanupAndRestart` exposed a separate
cleanup defect. It delays client creation until the API has returned failure,
removed the reservation, and deleted the Gateway. The delayed request then
creates a client. Previously, that client survived restart because recovery
excluded the deleted account record.

Recovery now includes deleted records for failed, deleting, and abandoned
accounts. These records retain the stable Gateway and account IDs. The task
repeats provider cleanup by those IDs, without requiring a live parent Gateway
or using an obsolete provider UUID. Normal API queries still exclude deleted
records. STEGO's existing trusted recovery query supplies this behavior.

The real-provider test now passes after restart and retains the account's audit
history. `TestDeletedServiceAccountCleanupRetriesAcrossPages` also checks 101
deleted records, a failed first cleanup attempt, and a live account that must
remain unchanged. It also puts a current failed account after the history in ID
order. A mixed scan delayed that account; separate cursors now keep current work
ahead of historical checks. Both passes share the four-second scan deadline.
Recovery retains a 100-row limit per pass and an eight-worker concurrency limit.
The existing transport deadlines still apply. Deleted account records currently
remain available for repeated cleanup.
Large-history capacity and a bounded retention policy remain unverified.

`TestKeycloakGatewayAudienceBinding` reproduced an access defect through the
running REST API and the generated provisioner runtime. A second Gateway owner
could not read the first Gateway, but could obtain a verified admin token for
its audience. The binding check now refuses that request before client creation.
Provider tests also cover missing attributes, a foreign ID, and a different
client ID for both creation and repair.

The same workflow exposed a second defect. Invalid OIDC settings prevented role
reduction after an owner became a viewer. The old admin credential stayed active.
Recovery now commits `revoking` state and its audit before terminal cleanup when
role reduction fails. This also covers loss of the provider binding. Restored
configuration or owner access cannot cancel committed revocation. Real-provider
tests check this after process restart. A separate database test proves that
terminal intent survives failed cleanup and a new service instance.

`TestRoleReductionCannotRiseAfterFailedCompletion` exposed another ordering
defect. The provider accepted the lower role, but the completion audit failed.
Restored owner access then let recovery raise the credential back to admin.
Recovery now stores the lower role with the pending state, before the provider
call. The test also covers pending records from the earlier implementation.
Both paths keep the lower role after completion failure and a new service instance.

The current design assumptions are a control-plane binding and terminal
revocation after failed role reduction. The user was asked about both choices.
A temporary provider error can thus require a new credential. If a deadline or
database failure prevents the state commit, the pending reduction remains for
recovery. Provider outages can delay cleanup. Existing access tokens remain
valid until expiry. Production recovery latency is not yet established.

Set `STEGO_REQUIRE_KEYCLOAK=1` to require this test. Docker must be available.
`scripts/check-gateway.sh` and CI require it. The container is isolated, uses
test credentials, and exposes only its TLS port on the loopback interface.
It uses the development profile and an embedded database. These are test setup
choices, not production deployment instructions. The first successful local run
completed in 33.14 seconds with Go 1.26.8 and PostgreSQL 18.6.

Remaining work includes automatic scans for provider drift and orphan clients,
production administrator permissions, recovery capacity, discovery of provider
objects with no retained database record, and other external failure cases. The delayed-update test
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

The expanded local race suite passed with PostgreSQL and Keycloak required. The
acceptance package completed in 210.318 seconds. The later role-completion
correction passed focused race tests in 11.978 seconds. Pinned regeneration had
no output changes or drift, and dependency verification passed. CI requires all
these tests on the final commit. These are correctness checks, not production
capacity measurements.

The first hosted run of this gate found a GORM schema data race. A Gateway
request and account recovery initialized related model metadata at the same
time. The race detector stopped the generated API process. The compiler pin now
includes schema preparation before the store becomes available to either path.
This changes no database tables and requires no startup migration. Generated
startup handles preparation errors before listeners or recovery tasks start.
STEGO also has an independent Record/Membership test for cold schema caches.

The full local race suite then passed with PostgreSQL and Keycloak required;
the acceptance package completed in 211.673 seconds. The final compiler pin
also includes an HTTPS completion check. Canceled or expired requests return
no response data, even when the body read completes at the cancellation boundary.
The compiler tests control this ordering and check resource cleanup. The final
variant CI run checks the complete application gate with both compiler fixes.
The real Keycloak audience and role workflow passed in 38.247 seconds with both
fixes. Final regeneration had no output changes or drift. The final compiler
revision also rejects entity names that collide with its schema helper.
