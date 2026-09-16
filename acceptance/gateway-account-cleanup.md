# Historical Gateway account cleanup

This record describes the synchronous contract before the HTTP 202 decision on
2026-09-16. Its implementation description and test results apply to that earlier
contract. See [durable asynchronous deletion](asynchronous-deletion.md) for the
current implementation, tests, and open qualification gates.

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

The earlier Gateway workflow created three automation accounts, used each token
to read Gateway provider data, then deleted the Gateway. It checked that token
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

The current browser workflow now calls the account cleanup helper. Before this
change, that helper had no caller after the controller-local database conversion.
The rendered account test created, revoked, and deleted its own account before
Gateway deletion. It did not prove cleanup of live accounts on Gateway deletion.

The added check creates three accounts through the generated browser backend.
Each account must obtain a token and read provider data from the actual Gateway.
After the Gateway DELETE response, all three credentials must fail token
issuance, and their Keycloak clients must be absent. Each account must have a
closed metadata row and exactly one successful Gateway cleanup audit. The check
runs before the existing SQL cleanup denial and recovery checks. Evidence
contains account IDs and counts, with no credentials. It does not claim that
previously issued access tokens are erased.

The [account-source API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34951393810)
passed all 30 required checks at `10a0827`. Verification matched 887 source
files, 229 generated files, all generation records, and cleanup. This compiles
the complete acceptance package but does not execute the new live account
checks. Browser run `34951393842` subsequently passed both viewer recovery and
account deletion. See the [verified evidence](browser-viewer-recovery-evidence.json).
The historical results above do not cover the added checks.

The complete supplied PostgreSQL browser workflow passed in 395.99 seconds at
`10a0827`. All three automation accounts used the actual Gateway. After the
Gateway DELETE response, token issuance failed, all three provider clients were
absent, and each account had closed metadata and one successful cleanup audit.
SQL denial and recovery, normal deletion, and automated cleanup also passed.
Verification matched all 887 source files, 230 generated files, and three
generation records. Independent reads confirmed that the Job and Pods were
absent. This result does not claim immediate invalidation of issued tokens.
