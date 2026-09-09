The Gateway workflow is the first application acceptance gate. Run it with
`scripts/check-gateway.sh`. Set `STEGO_TEST_POSTGRES_DSN` to a PostgreSQL
connection that can create test databases. The command fails if this setting
is absent. Docker is also required for the separate service-account workflow
against Keycloak. It runs the same checks as CI:

1. Verify Go dependencies.
2. Fetch and build the pinned STEGO compiler.
3. Apply generation, resolve dependencies, apply again, and check output drift.
4. Check that generated output matches the committed files.
5. Run all tests with PostgreSQL required and the race detector enabled.

The generated application runs in separate processes. These processes also use
the race detector when the tests use `-race`. A detected race stops the process
with an error. Tests without `-race`, including performance measurements, build
the application without this instrumentation.

| Required behavior | Executable evidence in `acceptance/` |
| --- | --- |
| Create and retrieve with required IDs and API shapes | `TestGatewayCreationCommitsOwnerAndEvent` checks the KSUID, derived namespace, placement, and timestamps. `TestGatewayWorkflowThroughGeneratedRESTProcess` checks the pinned response schema. `TestGeneratedGatewayDescriptorsMatchReference` checks wire descriptors. |
| Commit the Gateway and owner grant together | `TestOwnerGrantFailureRollsBackGatewayAndEvent` and `TestEventFailureRollsBackGatewayAndOwner` inject database failures. Both transport workflow tests also inject grant and event failures. |
| Enforce access and filter lists | `TestAccessFiltersRunBeforeCountAndPagination` checks database filtering. Both transport workflow tests check owners, viewers, removed grants, denied requests, hidden resources, and list totals. |
| Deliver the resulting event through the generated runtime | Both transport workflow tests consume the created event from a Kafka protocol fixture. They check its resource key, payload, kind, and message ID, then wait for the acknowledged queue entry to be removed. |
| Preserve behavior through REST, gRPC, restart, and regeneration | `TestGatewayWorkflowAcrossRESTAndGRPC` creates through each transport and reads through the other. Both transport tests restart the generated process and check access. `TestGeneratedRuntimeDeliversGatewayEventsAcrossRestart` commits while the process is stopped and checks delivery after restart. The gate command checks regeneration before tests. |

STEGO supplies storage transactions, relation filters, generated transport
contracts, authentication, the process lifecycle, and event delivery. Hypershell
supplies Gateway placement, owner grants, API field mapping, and access rules.
STEGO has separate Record service tests for its common contracts.

The test setup creates private databases and applies generated schemas. It uses
signed test tokens and a Kafka protocol fixture with mutual TLS. The gate does
not prove production schema upgrades, broker failover, cluster provisioning,
production capacity, or complete Hypershell compatibility. See
[the broader evidence and remaining work](README.md).

Use application failures to select further infrastructure changes. A new common
capability must address a demonstrated requirement and have a separate service
test. Passing this gate does not complete the enterprise readiness goal.

The complete gate passed locally on 2026-09-08 with Go 1.26.8, PostgreSQL 18.6,
and compiler revision `77290f7697c75f73b200253700aea754437c3c34`. Regeneration
had no changes or drift. The acceptance package completed in 63.671 seconds;
all other packages passed or had no tests. The command also rejected a missing
database setting. The descriptor check passed without the race detector, which
also checked the alternate test build configuration.
