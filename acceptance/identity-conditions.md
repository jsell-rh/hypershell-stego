Gateway declares `conditions: {identity: [ClientReady]}`. STEGO generates the
storage column, validation, owner checks, conditional writes, generation checks,
and transition times. Hypershell selects the condition name, provider actions,
access rules, and fixed reason text. It adds no condition storage framework.

`ClientReady` describes the Gateway identity client. It does not assert that all
user grants are synchronized. Grant changes need separate dependency checks
before a condition can describe that work. A current condition means that the
stored observation matches the desired generation. It does not prove controller
liveness or place a limit on observation age.

The controller always calls the provider for a live Gateway. It records `True`
with reason `IdentityClientReady` after the provider returns configuration.
A provider error produces `Unknown` with reason `IdentityProviderUnavailable`.
A work deadline produces `Unknown` with reason `IdentityObservationTimeout`.
An unreachable provider does not prove that its client is absent or broken.
The API maps these reasons to fixed messages. It never stores provider error
text in a condition. The existing generated observation runtime reserves time
for this write after the provider deadline.

The private `ObserveGatewayIdentity` RPC requires a configured controller subject
and its exact `Gateway/configure.identity` grant. It requires the resource
revision read before provider work. The service locks the live Gateway and
rejects a changed revision. OIDC configuration, the condition, and one event
share a transaction. If OIDC changes, the service records the condition at the
resulting generation while it still holds the row lock. A failed event insert
rolls back all changes. An unchanged current result causes no write or event.

`GetGatewayIdentityState` returns conditions by owner and name. An older desired
generation or a deleted Gateway has `Unknown/ObservationPending`, `current=false`,
and no transition time. Stored history remains intact. PostgreSQL sets transition
time when status changes. A reason change with the same status preserves that
time. The API returns it as UTC RFC 3339 text.

The application regression first failed because the recovery API had no durable
condition after provider failure. `TestIdentityProviderFailureHasDurableCondition`
now uses PostgreSQL, TLS gRPC, the generated controller runtime, and a provider
with controlled results. It verifies failure evidence across API restart,
fixed safe messages, generation invalidation, stale-write rejection, denied
writers, recovery, stable writes, deletion, and atomic rollback on event failure.
Real Keycloak workflows remain separate required checks.

Apply `out/storage/migrations/000007_resource_conditions.sql` before the new API
when migrations run externally. Restart API database connections after the
schema change; old prepared queries use a different row shape. This procedure
does not establish a deployment without downtime. Adding conditions changes the
resource contract and invalidates earlier generation observations. Migration
rejects removal of an owner or name with retained evidence. Startup checks the
column and resource trigger. Deploy the new API before the identity controller.
A missing condition contract stops live provider work.

Conditions for other controllers, grant dependency tracking, distributed
ownership, provider fencing, durable retry schedules, safe history retirement,
and production capacity remain open. A condition is not a provider lease.

With compiler `9633cdcdb9e8ef077b501844bce29dea13aa7f07`, all 13 selected
application tests passed in 142.341 seconds under race detection. PostgreSQL
and Keycloak fixtures were required. The tests cover Gateway and owner-grant
creation, event rollback, REST and gRPC access, identity provisioning and
cleanup, stored-grant login, API and controller restart, bounded cursor reads,
provider deadline recovery, compiler version, and the new condition workflow.
Unit tests, contract tests, and `go vet ./...` also passed. These local checks
do not replace the full application suite or Kubernetes workflow gates in CI.

Pinned regeneration preserved all 89 generated, state, and dependency file
hashes. Both STEGO condition commits passed CI.

`TestIdentityConditionDuringProviderTimeoutAndDesiredChange` adds a controlled
provider call inside the actual controller. It first establishes a ready client,
then withholds the next provider response until the work deadline. The API
records current `Unknown/IdentityObservationTimeout`, preserves the existing
OIDC configuration, and delivers the event through the generated runtime.
The condition and its transition time survive API restart.

The test also pauses a provider call while REST changes the desired Gateway
name. It releases the old result, then holds the next call as a barrier before
reading storage. The old result must change neither configuration nor condition.
A fresh pass recovers. Parent cancellation during a later call must leave the
last committed condition unchanged. The workflow passed under race detection
in 24.807 seconds with PostgreSQL and TLS gRPC. Its provider has controlled
results; it does not replace the separate real Keycloak checks.

A temporary Go compiler overlay replaced the stale-revision rejection with a
read of the latest revision. The new test failed at its old-result barrier in
23.066 seconds: the old configuration had been published as current and ready.
The overlay did not change source files. This verifies that the test detects an
incorrect attempt to commit an old result with a newer resource revision.
Static checks for the acceptance package also passed.
