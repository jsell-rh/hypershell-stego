The Gateway workflow is the first application acceptance gate. Run the full
suite in [hosted CI](../.github/workflows/checks.yml), not on the developer
workstation. CI runs `scripts/check-gateway.sh --suite=core` and
`scripts/check-gateway.sh --suite=browser` in separate jobs.

The command requires `STEGO_TEST_POSTGRES_DSN` with permission to create test
databases and a readable `STEGO_TEST_POSTGRES_CA_FILE` for the TLS fixture.
It fails if either setting is absent. Keycloak tests also require Docker.
The browser job supplies a separate, bounded Chromium fixture. The checks are:

1. Verify Go dependencies.
2. Prepare the authenticated compiler package and check its pinned digest and
   source identity. The consumer does not build the compiler.
3. Apply generation, resolve dependencies, apply again, and check output drift
   for the API and both browser modules.
4. Check that generated output matches the committed files, including a check
   for untracked output.
5. Run the selected test suite with PostgreSQL and Keycloak required and the
   race detector enabled. The browser job requires a passing rendered workflow.

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

The current [database contract](external-gateway-databases.md) uses an
operator-supplied PostgreSQL server. Each Gateway gets a separate logical
database and restricted login. The API rejects `database_id`, including empty
and null values. The application has no database catalog, database provider
selection, CNPG installation, or deployment-backed database server.

The test setup creates private databases and applies generated schemas. It uses
signed test tokens and a Kafka protocol fixture with mutual TLS. The gate does
not prove production schema upgrades, broker failover, cluster provisioning,
production capacity, or complete Hypershell compatibility. See
[the broader evidence and remaining work](README.md).

Use application failures to select further infrastructure changes. A new common
capability must address a demonstrated requirement and have a separate service
test. Passing this gate does not complete the enterprise readiness goal.

## Historical results

The records below apply to their stated revisions. The deployment placement
and CNPG model in these records are retired. Their local commands are not
instructions for the current developer workstation. Use the CI workflows above
and [current database evidence](external-gateway-databases.md).

The complete gate passed locally on 2026-09-08 with Go 1.26.8, PostgreSQL 18.6,
and compiler revision `77290f7697c75f73b200253700aea754437c3c34`. Regeneration
had no changes or drift. The acceptance package completed in 63.671 seconds;
all other packages passed or had no tests. The command also rejected a missing
database setting. The descriptor check passed without the race detector, which
also checked the alternate test build configuration.

The gate also includes [Gateway grant changes](gateway-grants.md). These tests
use application grant methods instead of direct grant inserts for the new
workflow. They cover a missing storage rule found during re-grant: deleted rows
must retain history without reserving a live grant key.

The gate now includes [deployment placement](deployment-placement.md). Its new
workflow uses the reference default and commits a new database with the Gateway,
owner grant, and three events. The earlier shared-database workflows explicitly
select `DATABASE_PROVIDER=cnpg`. The real Keycloak browser workflow uses deployment
placement. Workload provisioning remains outside this gate.

The five-part Gateway API gate passed again on 2026-09-09 at variant commit
`cbfce8e025145900ea00bdf47ecaeef8f707116b`, with compiler revision
`e6b4d6ceb198c89c9ad4eaedb486a1b2e81e7037`. The command was
`scripts/check-gateway.sh`, with the test PostgreSQL connection configured.
It required PostgreSQL and Keycloak, enabled race detection, verified modules,
and checked pinned regeneration against the committed output. Regeneration had
no changes or drift. All packages passed; the acceptance package took 321.869
seconds. The environment used Go 1.26.8 and PostgreSQL 18.6.

This result covers all five rows in the evidence table. The generated process
serves REST and gRPC and delivers the committed events. Hypershell supplies its
placement and access rules. Separate database workload tests also passed with
the generated Kubernetes client; see [their results](database-workflow.md).
The later [Gateway workload gate](gateway-workload.md) tests actual OpenShell
startup and provider management. Sandbox execution remains open. The Kafka
protocol fixture does not establish production broker behavior or capacity.

Hosted [variant checks](https://github.com/jsell-rh/hypershell-stego/actions/runs/34314163495)
passed both the full acceptance job and the database workflow job on this code.
The [pinned compiler checks](https://github.com/jsell-rh/stego/actions/runs/34314041752)
also passed.
