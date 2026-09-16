This repository is the test bed for a fully STEGO-based Hypershell.
The port and enterprise qualification remain active. A passing workflow applies
only to its recorded source and configuration.

STEGO supplies common storage, transactions, events, authentication, telemetry,
HTTP and gRPC runtimes, browser sessions, clients, and controller mechanisms.
Hypershell supplies Gateway ownership, grants, placement, account policy, and
OpenShell configuration. Common components must not depend on Hypershell entity
names. The compiler revision is pinned in [.stego/compiler-revision](.stego/compiler-revision).
Generated code must not be edited by hand.

## Current application behavior

Gateway creation commits the Gateway, owner grant, and events together. REST
and gRPC use the same access rules, including filtered lists and denied reads.
Generated event delivery retains work across restart. Durable Gateway deletion
returns HTTP 202 after it saves the request, blocks new accounts, and leaves the
Gateway visible as deleting until cleanup completes.

Controllers recover retained accounts, encrypted provider journals, SQL state,
and workload state. Provider discovery uses bounded pages and saved scan
failure. It checks each current client before it saves closure intent. A later
clean inventory and an atomic journal-scope guard are required for completion.
A provider limit cannot become a successful empty page. See the
[deletion contract](acceptance/asynchronous-deletion.md) and
[current discovery evidence](acceptance/provider-inventory.md).

The management console uses a separate generated Go browser backend. STEGO
provides sessions, encrypted tokens, OAuth, confirmed console and provider
logout, the API proxy, and browser telemetry. OAuth tokens stay out of browser
JavaScript. Hypershell retains the React application and its domain behavior.
The separate upstream per-Gateway dashboard is still an integration requirement.

The API has no database catalog or `database_id`. Installation supplies
co-located external PostgreSQL or CNPG. Controllers create an isolated logical
database and login for each Gateway. The external-server tests use PostgreSQL
fixtures; they do not create RDS. Public Gateway TLS uses router passthrough and
an operator-selected issuer. Gateway namespace traffic is restricted to declared
operator-approved destinations. Native external DNS enforcement remains a
separate qualification requirement.

## Verification state

| Source and check | Result and scope |
| --- | --- |
| `b58d9a2`, [API run 35103602751](https://github.com/jsell-rh/hypershell-stego/actions/runs/35103602751) | All 49 required checks passed. REST, gRPC, transactional events, retained cleanup, concurrent journal registration, and regeneration passed. Test cleanup was independently checked. |
| `5753c33`, [discovery run 35105755539](https://github.com/jsell-rh/hypershell-stego/actions/runs/35105755539) | Eight SQL and two boundary tests passed. They include a failed first read, independent later clients, store reconstruction, shifted pages, and foreign-client preservation. |
| `d1ec34c`, [full run 35102064260](https://github.com/jsell-rh/hypershell-stego/actions/runs/35102064260) | Core, rendered browser, image, and console checks passed. This source predates the scope guard and bounded provider discovery. CNPG and Sandbox were not selected. |
| `bceea63`, [CNPG run 35100459235](https://github.com/jsell-rh/hypershell-stego/actions/runs/35100459235) | Complete Gateway workflow passed with primary replacement, stable SQL identities and data, regeneration, and verified cleanup. This source predates journal enumeration. |
| `550b2b7`, full and public browser workflows | Runs `35104550272` and `35104667835` are active at this record. They do not qualify later bounded discovery. |
| `85706c6`, [maximum-offset recovery](acceptance/provider-window-sql-evidence.json) | All twelve required checks passed in `35106173055`. The latest full run `35106305055` remains pending. The API gate now requires 51 checks. |

These results do not establish complete parity, production capacity, backup and
restore, or every deployment recovery case. The user deferred the live Kata
test because no suitable cluster is available. Keep the provisioner at one
replica with `Recreate`, stable journal keys, and its exact private API grants.
Cross-process writer fencing and whole-database rollback detection remain open.
The current database model requires a fresh schema or explicit operator teardown
and recreation; startup never performs that teardown.

## Repository layout

| Path | Responsibility |
| --- | --- |
| `service.yaml`, `registry/`, `.stego/` | Application declaration, component metadata, compiler pin, and generation state |
| `out/` | Generated API, storage, controller, client, security, and telemetry code |
| `internal/` | Domain policy and application adapters |
| `contracts/` | Reference contracts and explicit application extensions |
| `console/` | Separate generated browser backend and its declaration |
| `components/web-console/`, `packages/gateway-management-ui/` | Management UI and domain components |
| `acceptance/`, `.github/workflows/`, `scripts/` | Required checks, bounded CI runners, and source-specific evidence |

Use the [full contract workflow](.github/workflows/checks.yml),
[API workflow](.github/workflows/jshell-gateway.yml),
[public browser workflow](.github/workflows/jshell-browser.yml), and
[CNPG workflow](.github/workflows/jshell-cnpg.yml) for qualification.
Run heavy checks in CI or the restricted jshell environment. Use the saved
context explicitly, keep one live cluster test at a time, and verify cleanup
before the next test. Do not use Playwright or run performance tests on the
workstation. A missing result is not a pass.

The [acceptance index](acceptance/README.md) links each contract and earlier
results. The [previous overview](acceptance/repository-history-20260916.md)
preserves the implementation history. The full STEGO goal remains active.
