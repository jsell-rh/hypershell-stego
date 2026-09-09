# Gateway account cleanup

Deleting a Gateway also removes its automation identities. REST and gRPC use
the same domain operation and generated row lock. Authorization precedes all
provider calls. An account creation already in progress must finish before
cleanup obtains the lock. A later creation sees the deleted Gateway.

The provider first disables and removes all managed clients for the Gateway.
This includes clients with no account row. Each client must have matching
ownership attributes and the exact client name derived from its resource IDs.
A client for another Gateway is left unchanged.

After provider cleanup succeeds, one database transaction removes visible
account metadata, records one non-secret cleanup audit per account, removes the
Gateway, and stores its deletion event. The generated runtime delivers the
event. The account tombstones retain `deleting` state so recovery can remove
late provider results without a live Gateway.

Provider failure returns HTTP 503 or gRPC `Unavailable`. The Gateway remains
visible. The caller can retry after the provider recovers or the API restarts.
An audit or event failure rolls back all database changes. It cannot undo
provider removal. A retry verifies the provider state again before commit.
No failed cleanup response contains provider details or credentials.

A configured provider is required if any account history exists. If the provider
is disabled and no account history exists, Gateway deletion can proceed. Domain
services constructed without a cleanup service still reject live accounts.
Both generated API transports configure the cleanup service.

The gRPC registration factory owns its provider client through STEGO's
`OnClose` hook. The runtime closes that client after normal stop, failed startup,
or explicit close. HTTP uses its existing managed application lifetime.

The first test created three accounts but Gateway deletion returned HTTP 409.
The completed transport test covers both REST and gRPC, denied access, provider
failure, event failure, restart, retry, deletion events, and late provider
results. A concurrent test checks creation against deletion under the generated
row lock. A real Keycloak test removes two stored clients and one orphan after
an outage and restart, while another Gateway credential continues to work.

The actual Gateway workflow creates three automation accounts, uses each token
to read Gateway provider data, then deletes the Gateway. It checks that token
issuance fails for all three accounts before workload teardown. This test found
a readiness mismatch: the controller reported `ready`, while account creation
requires `Running` and `Healthy`. The controller now writes the phase and status
together. An unavailable workload cannot retain healthy state.

Provider calls retain the generated five-second deadline. Realm inventory has
a 10,000-client scan limit. Large-realm cleanup capacity and latency still need
measurement. Existing access tokens are not remotely erased; Gateway teardown
removes the serving workload, and tokens otherwise expire under their policy.

The complete local Gateway workflow passed in 177.50 seconds. Recovery and
workload tests together took 235.178 seconds with the race detector. The full
variant suite passed; its acceptance package took 392.445 seconds. Focused checks
then passed against compiler `95ff57f6a02bd0d0a2808bbe7da8e0f72da7861c`, including
the corrected health state and real Keycloak cleanup. Pinned regeneration
reported no changes or drift. Static checks also passed.

One local cleanup of two stored clients and one orphan took 412.8 ms through
REST, the generated RPC client, and real Keycloak. This is one observation,
including connection setup after restart. It does not establish production
capacity or latency percentiles.
