The service-account workflow now uses a real Keycloak provider. The application
creates accounts through REST. A separate provisioner process serves the pinned
internal RPC contract through STEGO's generated TLS runtime. Hypershell owns
client settings, role mappings, audience mappings, ownership checks, and lifecycle
operations. STEGO owns HTTPS limits, JWT signature verification, RPC transport,
process signals, telemetry, and cleanup. See the [RPC process check](rpc-process.md).

Run `go run ./out/grpcapi/processes/provisioner` with these settings:

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
input. Provider failure fixtures use their administrator to set the binding. The
[Gateway identity workflow](gateway-identity.md) uses the real controller to create
the binding. Existing-client migration and the workload controller remain open.

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

## Common service-account role operations

The adapter uses STEGO's `InspectServiceAccountRoles` and
`ReconcileServiceAccountRoles`. Hypershell supplies the trusted account and
Gateway bindings and selects the OpenShell roles. STEGO checks the saved
service-account subject, exact direct and effective roles, and group membership.
Role changes require a disabled client. Excess realm, client, and group access
must be removed before additions. A final role inspection also requires the
provider user to be enabled before the application continues.

This replaces the previous direct-role-only convergence check. The real
service-account workflow now adds a group without changing direct roles or
client configuration, then requires reconciliation to remove that group.
The test must pass in CI before this application change is qualified.

The handwritten main adapter fell from 1,014 to 972 lines. The three main
adapter files now total 1,314 lines. The small adapter suite passed with the
race detector in 1.311 seconds. API and console generation used the clean
compiler pin `fdd5efefd8e0631005c890bd1c44fe01680f2804`; both drift checks passed.

Scope and mapper adoption remains tied to the complete client lifecycle.
A full client update can restore Keycloak's shared `service_account` scope.
STEGO's complete access operation uses a minimal enable update and checks the
full policy afterwards. The application still needs durable legacy bindings,
ownership migration, and adoption of that complete operation. The role check
alone does not prove token or client configuration security.

## Credential formatting

The provider, migration checkpoint, credential response, and client wrapper use
`fmt.Formatter` for redaction. Numeric formatting verbs can bypass `String` and
`GoString`; the regression test reproduced disclosure with `%d` before the fix.
Seven formatting forms and implicit JSON export are now checked. The small
adapter suite passed with the race detector in 1.327 seconds. The application
uses clean compiler pin `436e43dfe57e52e4c3396d5b633e4645fd3d2841`.
The new resource-state migration is not part of this pin.

## Complete realm assignment reads

Compiler pin `688d91bc597589a42bc651d0957688ac3a064b5a` supplies provider
version 0.10.4. Keycloak filters its combined role-mapping response by role-view
permission. The Hypershell provisioner can manage users and clients, but lacks
realm-view permission. The combined response can therefore hide the service
account's default realm role. The common role operation detected excess effective
access and stopped, so account creation failed.

STEGO now reads the dedicated realm-mapping endpoint before it changes roles.
That endpoint requires user-view permission. Hypershell keeps the same provider
permissions. A denied or contradictory direct read stops repair before a write.
The change is common provider code; Hypershell has no new role-reading mechanism.

A new real-provider test reproduced the failure in STEGO CI run
[35038361635](https://github.com/jsell-rh/stego/actions/runs/35038361635).
The regression uses client roles only, the Hypershell permission set, distinct
provider and public client IDs, and legacy ownership keys. Both generated unit
variants pass after the fix, with the race detector, in 14.373 seconds. The
Hypershell adapter suite passes in 1.328 seconds. The real-provider suite passed in 63.85 seconds in STEGO CI run
[35038752735](https://github.com/jsell-rh/stego/actions/runs/35038752735).
The restricted client-role operation and inspection took 543 milliseconds.
The rendered browser workflow passed in 74.93 seconds in
[application job 104614812988](https://github.com/jsell-rh/hypershell-stego/actions/runs/35038888231/job/104614812988)
on application commit `5144e666eb7a9cf61f2ba1a6070cd8815568ba93`.
This run includes the fixed common provider. The full cluster workflow, CNPG
workflow, and later protected-journal API checks have separate pending results.

Browser test failures now retain only the fixed provisioning operation's status,
duration, and transport side from generated OTEL spans. The test does not print
arbitrary attributes, error text, the DOM, or one-time credentials.

## Full cluster workflow after the account fix

The complete jshell Gateway browser workflow passed in
[35038887851](https://github.com/jsell-rh/hypershell-stego/actions/runs/35038887851)
on application commit `5144e666eb7a9cf61f2ba1a6070cd8815568ba93`.
The saved cleanup record confirms that test resources and namespace allocations
are absent; the restricted CI namespace remains. This proves the earlier account
fix in the full cluster workflow. It does not qualify the later native lifecycle
and key-file changes.

The common native lifecycle passed the real-Keycloak suite in
[STEGO job 104617779509](https://github.com/jsell-rh/stego/actions/runs/35040036218/job/104617779509).
The suite took 56.84 seconds. It includes new creation, legacy migration, a lost
journal acknowledgement, recovery with a new journal, complete access policy,
and retained cleanup. Hypershell now calls this lifecycle from its production
identity worker. The new application workflow still requires a CI result.


## Restricted native lifecycle and abnormal exits

STEGO CI run `35040645391`, provider job `104620219781`, passed the real native
lifecycle with `manage-clients`, `view-clients`, `manage-users`, and `view-users`.
It used no `view-realm` or `manage-realm` grant. The test covered creation,
legacy migration, a lost journal acknowledgement, a new journal after restart,
complete access policy, and retained cleanup. The whole real-provider test took
58.01 seconds. This is provider evidence; the updated Hypershell workflow is
still pending.

Hypershell now pins compiler `3643e4ee16dcc07c44e32fc5901e4bf9c7edcf9d` and provider
`0.11.2`. The common access operation attempts bounded disablement after a panic
or `Goexit`. It preserves that abnormal exit and releases the operation permit.
The generated regression and access tests passed with the race detector in both
variants in 11.746 seconds. Both application generation drift checks passed.
The application adapter and identity-controller tests passed with the race
detector in 1.342 and 22.283 seconds.

The service-account adapter still needs the complete common access lifecycle.
Its current full-record enable update can restore a shared Keycloak scope, so
scope and mapper adoption must include the enable operation. The API already
commits an account reservation before the provider call. It saves the provider
client ID and subject only after that call returns. The next change must add
saved provider creation and migration state to that reservation, preserve the
one-time credential response, and keep cleanup possible after a lost response.
Gateway roles, account quotas, expiry, and creator authorization remain
application policy. No service-account lifecycle migration is claimed here.

## Legacy audience names

The native lifecycle change made the service-account role path require the
new generated Gateway client name for legacy ownership as well. A focused test
reproduced rejection of an otherwise valid stored legacy audience. The account
path now accepts the exact stored public name when the provider client has the
complete legacy Gateway ownership attributes. It retains the provider ID and
Gateway ID checks. New ownership still requires the generated name, and the
common provider still rejects unknown reserved ownership keys and partial
migrations. The adapter suite passed with the race detector in 1.368 seconds.

The shared service-account lifecycle passed its real Keycloak check in STEGO
run `35041366927`, provider job `104622531905`. The whole real-provider test took
61.44 seconds. The account checks covered creation, migration, a saved subject,
a lost journal acknowledgement, restart, signed token policy, disabled repair,
resume, and late-create cleanup. Production provisioner adoption remains open.

## Common account lifecycle integration

The production account provisioner now uses STEGO provider 0.13.0 for creation,
legacy ownership migration, saved subjects, complete access repair, and retained
cleanup. The handwritten administrator HTTP client and token cache are removed.
Hypershell retains Gateway ownership, OpenShell roles, account IDs, and one-time
credential response policy. People and authorized API automation follow the
same Gateway owner and viewer rules.

The [protected journal instructions](service-account-provider-state.md) describe
the required API identity, keys, and instance ID. The common provider passed its
real Keycloak test in run `35042039359`. The new application integration has
compiled and passed the small adapter race checks. Its live gates are pending.
Earlier results in this file do not qualify this migration.

## Native lifecycle cluster result

The complete cluster browser workflow passed on application commit
`6354a23c47f7627b779d637bb0dd3d6e93ca53d0` in
[run 35041052519](https://github.com/jsell-rh/hypershell-stego/actions/runs/35041052519).
The test took 501.61 seconds. It covered login, Gateway creation, owner and
viewer access, REST and gRPC, events, worker and API restart, SQL recovery,
service-account use of the real Gateway, and deletion. The saved cleanup record
confirms that test resources and namespace allocations are absent. See
[native lifecycle evidence](native-lifecycle-browser-evidence.json).

This result precedes the common service-account lifecycle migration. For that
migration, the fixed CI receiver now permits TCP 19094 from the provisioner.
The update ran with the live-test Lease held and no test workload present.
All fixture network policies passed verification, and the Lease was released.
See [receiver update](account-state-receiver-update-20260916.json).

## Account journal storage correction

The rendered browser test in run `35045331844` failed during account creation.
The journal check called the versioned retained-read method for ServiceAccount,
which has no resource version. A focused test reproduced that rejection. The
check now uses the common retained cursor, bounded to the exact account ID.
Foreign Gateway accounts are still denied. The focused tests passed with the
race detector in 1.021 and 1.027 seconds. Live qualification remains pending.

The earlier run `35045188547` stopped at stale generation records. Those records
were regenerated with the pinned compiler for both the application and console.
Run `35045331844` then passed regeneration, web-console checks, and image checks.
Its failed browser result does not qualify the account lifecycle migration.

## Corrected account browser result

The rendered browser workflow passed on commit
`048ff55bf2a26dbfac3e238fec3352376fba6495` in
[job 104635435888](https://github.com/jsell-rh/hypershell-stego/actions/runs/35045870530/job/104635435888).
It took 104.29 seconds. The test covered real login, Gateway creation and grants,
REST and gRPC, events, restart, session key rotation, renewal, and logout.
Account creation, one-time credential delivery, verified token issuance, reload,
revoke, and delete all passed with the common lifecycle and SQL journal.
This browser fixture supplies Gateway readiness. The full cluster and private
API results remain pending. Web-console and generated image checks also passed.

## Common account lifecycle cluster result

The [complete cluster workflow](common-account-lifecycle-20260916.md) passed on
`048ff55` in 524.89 seconds. It used the common account lifecycle and SQL journal,
replaced the provisioner Pod, used browser-issued credentials on real Gateways,
and removed three provider clients before the Gateway deletion response.
The saved cleanup record confirms that test resources and allocations are absent.
Private state API, core-suite, and current CNPG qualification remain pending.

## Legacy orphan fixture correction

Core job `104635436006` finished in run `35045870530` with one failure in
`TestGatewayDeletionWithProviderFailureAndOrphans`. The real account workflow
passed in 53.36 seconds. The private state API test passed in 6.04 seconds.

The orphan fixture omitted the current ownership keys from an attribute update.
Keycloak patches that map, so omitted keys can remain beside the legacy keys.
The fixture now checks rejection of mixed ownership, removes the current keys
with explicit empty values, and reads the saved legacy attributes before it
tests outage, restart, and cleanup. Production ownership checks are unchanged.
The fixture compiles; its real-provider result is pending. Core CI now streams
individual test records so failures are visible before the package finishes.
