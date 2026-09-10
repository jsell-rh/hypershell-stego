Hypershell uses STEGO compiler
`facefeb2aacec8acf9fa15fef6a8bd8fbd4fc052` and controller 1.14.0 for recovery
sweep telemetry. The local controller manifest now records that version.
Service-account recovery supplies account rules and cursor pages to `RunSweep`.
The former Hypershell callback for common recovery logs has been removed.
The [compiler contract](https://github.com/jsell-rh/stego/blob/facefeb2aacec8acf9fa15fef6a8bd8fbd4fc052/specs/sweep-observability.md)
defines common signals, retry meaning, ownership, and limits.

`TestServiceAccountSweepTelemetryAcrossRestart` uses a stored Gateway fixture,
the generated application, PostgreSQL, a controlled TLS gRPC provisioner, the
generated event runtime, and a real TLS OTLP collector. It performs these steps:

1. Create a service account through REST and receive its one-time credential.
2. Receive the Gateway event and await an empty outbox.
3. Fail provider changes and request account revocation through REST.
4. Require common retry telemetry from the generated recovery sweep.
5. Stop the API and verify that the revocation request remains stored.
6. Restore the provider and restart the API on the same database.
7. Require completed revocation and removal of the provider identity.
8. Verify owner access, denied access, and credential exclusion from reads.
9. Stop the API and verify successful recovery telemetry.

Both runs must produce local recovery records, page spans, action spans,
correlated action logs, and duration metrics. All controller signals in a run
must use the same service instance ID. The replacement run must use a new ID.
Local and exported data must exclude the account credential, token, Gateway and
account IDs, account name, and private provider error text. The test checks
retrying failure before restart and success after restart.

The baseline failed on compiler `f7a630b`: the pending revocation had no common
retry telemetry. With this compiler, the new workflow passed in 8.10 seconds.
Fifteen selected application tests passed under race detection with PostgreSQL
and Keycloak required, in 134.656 seconds. They include the real Keycloak account
workflow, recovery across deleted and partial pages, role reduction, controller
recovery, request signals, HTTP diagnostics, process privacy, task abort and
restart, and watch-source shutdown. The Keycloak workflow took 34.02 seconds.
Internal and contract race tests and static checks passed. Repeat generation
preserved all 97 output, state, and dependency hashes.

Compiler CI run 34537660418 passed. The earlier application CI result and its
startup assertion correction are recorded in [CI evidence](ci-evidence.md).
The new application revision needs its own full CI result. Kubernetes and
virtual-machine gates were not repeated locally for this change.

This is a workflow result, not a production capacity result. The controlled
provisioner supplies faults for the telemetry test; the separate Keycloak test
supplies real identity-provider evidence. Standalone scans and cycles, the older
controller loop, database and outbound signals, process resource metrics, and
complete process lifecycle export remain open. The full enterprise goal remains
active.

The later [RPC client checks](rpc-client-observability.md) extend this workflow
with correlated outbound calls, status metrics, and shared runtime identity.
