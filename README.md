This repository is the test bed for a fully STEGO-based Hypershell.
The port and enterprise qualification remain active. A passing workflow applies
only to its recorded source and configuration.

STEGO supplies common storage, transactions, events, authentication, telemetry,
HTTP and gRPC runtimes, browser sessions, clients, and controller mechanisms.
Hypershell supplies Gateway ownership, grants, placement, account policy, and
OpenShell configuration. Common components must not depend on Hypershell entity
names. The compiler revision is pinned in [.stego/compiler-revision](.stego/compiler-revision).
Generated code must not be edited by hand.

All three generated modules use the common STEGO registry from the same pinned
Git revision as their compiler. Local registries contain application archetypes;
they contain no copies of common component declarations. Application output
paths are set in `service.yaml`. See the [registry model](registry/README.md)
and the [Gateway console registry result](acceptance/composed-registry-module-evidence.json).
The generated runtime stays in Git for review and repeatable builds.

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
The upstream per-Gateway dashboard uses its own generated Go browser backend.
STEGO supplies its authentication, deployment, lifecycle, browser client, and
telemetry. Its public and expanded CNPG workflows passed. The CNPG fixture now
permits database ingress from the separate console Pods through a narrow
[network rule](acceptance/dashboard-cnpg-network-evidence.json). The complete
[CNPG result](acceptance/dashboard-cnpg-workflow-evidence.json) includes primary
replacement, dashboard recovery, durable deletion, and independent cleanup.

The API has no database catalog or `database_id`. Installation supplies
co-located external PostgreSQL or CNPG. Controllers create an isolated logical
database and login for each Gateway. The external-server tests use PostgreSQL
fixtures; they do not create RDS. Public Gateway TLS uses router passthrough and
an operator-selected issuer. Gateway namespace traffic is restricted to declared
operator-approved destinations. Native external DNS enforcement remains a
separate qualification requirement.

## Verification state

The latest main application checks use source `0175b0b` and compiler
`83592bee5a17de6936cf521b94629e2a225a8d37`. The
[public browser workflow](acceptance/startup-main-browser-evidence.md),
[API and core checks](acceptance/main-api-core-evidence.md), and
[CNPG workflow](acceptance/main-cnpg-workflow-evidence.md) passed. Both browser
workflows verified all 415 generated-file hashes and 48 matching startup
log/span pairs from six browser instances, with no failed startup pairs.
The CNPG run needed one secondary database Pod replacement for scheduling.
Independent cleanup passed. These records retain the limits of each check.

The following table records earlier checks. Their results and limits apply to
their listed source revisions.

| Source and check | Result and scope |
| --- | --- |
| `ca8814f`, [dashboard application checks](acceptance/dashboard-signout-full-evidence.json) | All 302 expected top-level tests passed. Four declared live tests were skipped. CNPG and Sandbox jobs were not selected. |
| `e8bb965`, [complete public dashboard workflow](acceptance/dashboard-public-workflow-evidence.json) | Passed in 670.99 seconds. Editor behavior, access, events, restart, sign-out, recovery, durable deletion, and linked logs, metrics, and traces passed. All 412 generated-file hashes matched. Independent cleanup passed. |
| `aed33a9`, [complete CNPG dashboard workflow](acceptance/dashboard-cnpg-workflow-evidence.json) | Passed in 768.33 seconds. Primary replacement preserved SQL identities, credentials, keys, and data. Dashboard, access, recovery, deletion, and linked telemetry passed. All 412 generated-file hashes and three viewed screenshots matched the final archive. Independent runtime and volume cleanup passed. |
| `0d74978`, [expanded CNPG dashboard workflow](acceptance/dashboard-cnpg-readiness-evidence.json) | Failed after 325.96 seconds because Gateway provisioning did not finish. Repeated generation and independent runtime and volume cleanup passed. Dashboard, primary replacement, and final application deletion remain unproved in this run. |
| `85706c6`, [full application](acceptance/core-window-evidence-20260916.json) | Core, rendered browser, image, and console jobs passed. The saved log has 483 passes and four named test exclusions. CNPG and Sandbox were not selected. |
| `120711a`, [API workflow](acceptance/api-window-evidence-20260916.json) | All 51 required checks passed, with no failures or skips. All four generation snapshots matched. Independent checks confirmed resource and Lease cleanup. |
| `d53f843`, [public Gateway workflow](acceptance/browser-window-evidence-20260916.json) | Passed in 498.58 seconds. All 269 generation hashes matched. Test and independent operator cleanup checks passed. |
| `e8ace19`, [CNPG Gateway workflow](acceptance/cnpg-window-evidence-20260916.json) | All ten required tests passed. The complete browser workflow took 470.85 seconds. Primary replacement preserved SQL identities, keys, credentials, and data. Regeneration and independent cleanup checks passed. |
| [SQL discovery recovery](acceptance/provider-window-sql-evidence.json) and [TLS provisioner recovery](acceptance/provider-wire-sql-evidence.json) | Both twelve-test gates passed. They cover failed inventory windows, protected state, recovery, and closure through the authenticated generated transport. |

The dashboard results use compiler `db75a77`. The earlier application results
use the runtime from `85706c6` and compiler `af67e7b`. They establish the earlier
Gateway deletion and provider recovery behavior. The earlier CNPG result does
not establish the expanded dashboard workflow. Historical results remain in
[the repository record](acceptance/repository-history-20260916.md).

The earlier CNPG run needed one manual replacement of its secondary database Pod to
free CPU for the existing test Job. One console then restarted three times
before it became ready, with no configuration change. Its log identifies
browser initialization but not the cause. The result does not prove startup
without assistance. The latest main workflows above include the generated
startup diagnostics added after this result. Production capacity remains open.

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
| `service.yaml`, `registry/`, `.stego/` | Application declaration and archetypes, pinned registry sources and compiler, and generation state |
| `out/` | Generated API, storage, controller, client, security, and telemetry code |
| `internal/` | Domain policy and application adapters |
| `contracts/` | Reference contracts and explicit application extensions |
| `console/` | Generated management browser backend and its declaration |
| `gateway-console/` | Generated browser backend for the upstream Gateway dashboard and its local composition |
| `components/gateway-dashboard/` | Upstream dashboard build inputs and integration files |
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
