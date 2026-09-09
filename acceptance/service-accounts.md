The service-account workflow now uses generated storage, HTTP endpoints, unary
RPC clients, and supervised recovery. Hypershell supplies the access rules,
quotas, lifecycle states, response fields, and provider protocol mapping.

The public routes create, list, retrieve, revoke, and delete accounts under
`/api/hypershell/v1/gateways/{gateway_id}/service_accounts`. Creation requires a
live owner or viewer grant. Owners can select `openshell-admin` or
`openshell-user`; viewers can select only `openshell-user`. A viewer sees only
accounts they created. Platform admin status supplies no implicit access.

Creation commits a non-secret reservation and audit record before calling the
provider. A Gateway row lock serializes reservations and enforces the quotas:
ten active accounts per creator per Gateway and 100 per Gateway. A nullable,
normalized active name supplies case-insensitive uniqueness. Terminal accounts
release their active names. The provider receives IDs derived from KSUIDs.

The provider result stays in memory. The application checks the current grant
again before it commits ready state and returns the secret. Later responses,
models, audit records, and events have no secret field. Failed creation returns
no credential. Stable Gateway and account IDs permit cleanup when a provider
reply is lost. Canceled creation attempts cleanup with a separate bounded
context. Recovery reclaims abandoned reservations after 15 minutes.

Revoke and delete first store their pending state. Terminal revocation removes
the Keycloak client and retains the account record and audit history. This
prevents a delayed update from enabling that identity again. A provider failure returns
HTTP 202. The generated supervisor runs recovery after restart. Recovery also
checks expiration and creator grants. A lost grant revokes the account. An owner
to viewer change lowers an admin account to `openshell-user`. The lower role
commits with pending state before the provider call. Failed completion cannot
restore the old role, including for pending records from the earlier implementation.
A failed downgrade
queues terminal revocation before provider cleanup. This prevents invalid identity
settings from preserving the old admin credential. A temporary provider error
can require a new credential. Restoring a grant does not reactivate
an account or raise its role. A terminal action and its audit record commit
together. Audit records survive soft deletion.

Recovery reads bounded pages and rotates through work categories. Each scan has
a four-second deadline and at most eight workers. Gateway row locks serialize
operations for the same Gateway. These bounds do not establish the reference
one-minute revocation target at production scale. Backlog capacity, recovery
indexes, counters, readiness, provider drift checks, and orphan discovery still
need work.

The task also revisits deleted records for failed, deleting, and abandoned
accounts. This removes a provider client whose creation completes after initial
cleanup. It uses stable resource IDs and does not require a live Gateway.
Current work initially runs before historical checks, with separate cursors and
a shared deadline. When a stream uses the pass budget, the next pass starts with
the next stream so that slow work cannot starve history. Deleted records remain hidden from normal queries. Large-history capacity and
cleanup-record retention need further verification.

The current provider settings are:

- `HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR`: host and port.
- `HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE`: trusted server CA.
- `HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE`: private bearer-token file.

The generated client requires TLS 1.3, validates the server identity, and reads
the service token for each call. Calls have a five-second deadline, 64 KiB message
limits, and a limit of 32 concurrent calls per client. Token replacement does not
require a process restart. An absent provider disables creation; metadata reads
remain available. Partial provider configuration fails startup. The proposed
internal authentication policy is TLS plus a verified service token and caller
subject allowlist. The user has been asked to confirm it or select mutual TLS.
The reference plaintext transport is not enabled in this variant.

The generated-process test uses PostgreSQL and a separate TLS gRPC provisioner
fixture. It verifies the service token and caller subject. Tests check the pinned
create and list response schemas, exact protobuf descriptors, secret exclusion,
service-token replacement, denied callers, pending revocation, and automatic
recovery after restart. Domain tests check audit rollback, lost provider replies,
access changes during creation, quotas, expiration, canceled creation, abandoned
reservations, and concurrent Gateway deletion.

The [real Keycloak workflow](keycloak.md) now checks client configuration, signed
token issuance, role reduction, drift repair, and revocation after restart.
It also proves that a delayed enable cannot undo revocation after the database
connection and its Gateway lock are lost.
The provider requires a trusted binding between the Gateway ID and its Keycloak
audience. Tests refuse foreign audiences and check terminal revocation after
invalid OIDC settings or loss of that binding. Restart and restored owner access
cannot cancel committed revocation.
The protocol fixture remains useful for controlled failures.
The [discovery workflow](service-account-discovery.md) covers search, status
filters, and custom ordering. Configurable expiration policy, deployment
manifests, SDKs, the STEGO CLI port, and web-console workflows remain open.

Gateway deletion now removes stored and orphan provider clients before it
commits account tombstones, cleanup audits, the Gateway tombstone, and its event.
The Gateway lock closes the race with account creation. See the
[cleanup workflow](gateway-account-cleanup.md) for failure and restart behavior.
Production migration management and upgrades from the reference table layout
remain open.

A local benchmark ran 100 complete create, get, revoke, and delete cycles. Each
cycle averaged 20.503 ms through a separate generated REST process and the TLS
provisioner fixture. It includes verified user and service tokens, database
transactions, audit writes, and terminal provider calls. It used Go 1.26.8,
PostgreSQL 18.6, and an Intel Core Ultra 9 185H. Process startup is outside the timed loop. The first cycle includes connection
setup. This is a local workflow baseline. It does not
measure Keycloak, concurrent capacity, latency percentiles, or server memory.
Run `go test -run '^$' -bench '^BenchmarkServiceAccountLifecycle$' -benchtime=100x ./acceptance`.

Service-account recovery now uses STEGO's generated `RunSweep`. Hypershell declares
nine state groups and twelve streams, plus their storage filters and domain
actions. STEGO owns the timer, cursors, page validation, worker pool, deadlines,
and group rotation. It validates the whole page before an action starts. Each
stream is limited to 10,000 page advances per cycle. Failed work stays eligible
through retained state; cursor progress does not acknowledge a provider effect.

A regression test uses 17 deleted records and keeps the first eight provider
calls slow until their contexts end. The former short-page reset prevented later
records from receiving a turn; that run failed after 20.41 seconds. The generated
sweep retains partial-page progress. The final test passed in 13.31 seconds.
A separate generated test proves that slow current work cannot starve history.

The focused application race suite passed in 157.916 seconds. It includes late
Keycloak creation and restart, revocation after database loss, the real Keycloak
account lifecycle, cleanup across pages, partial-page recovery, generated REST
and gRPC transports, role limits, and expiry. The compiler race suite and static
checks passed. These are correctness checks, not proof of production recovery
latency. The [sweep contract](https://github.com/jsell-rh/stego/blob/025aa22555b84d4e14ff8055f62eb7db9ee6107d/specs/controller-sweep.md)
records bounds, cursor ownership, and dispatch measurements.

Compiler pin `025aa22555b84d4e14ff8055f62eb7db9ee6107d` passed CI run
`34392328767`. Regeneration from that pin produced the same controller bytes used
by the application tests, and application static checks passed. The new remote
application gates run after publication of this migration.
