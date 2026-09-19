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
Git revision as their compiler. Only the API has a local application archetype.
Both consoles use the common browser archetype, including browser telemetry.
There are no local copies of common component declarations. Application output
paths are set in `service.yaml`. See the [registry model](registry/README.md)
and the [common browser composition record](acceptance/common-browser-composition.md).
The generated runtime stays in Git for review and repeatable builds.
Generation uses the [signed compiler package](acceptance/verified-generation.md)
through the common STEGO installer. All three modules use the same checked bytes.

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
telemetry. Earlier public and CNPG test results remain in the acceptance
records. The current database model uses only an operator-supplied PostgreSQL
server. See [the database contract](acceptance/external-gateway-databases.md).

The API has no database catalog or `database_id`. Installation supplies
co-located external PostgreSQL. Controllers create an isolated logical
database and login for each Gateway. The external-server tests use PostgreSQL
fixtures; they do not create RDS. Public Gateway TLS uses router passthrough and
an operator-selected issuer. Gateway namespace traffic is restricted to declared
operator-approved destinations. Native external DNS enforcement remains a
separate qualification requirement.

## Verification state

Main `034b46b` passed the complete hosted workflow in
[run 35460802241](https://github.com/jsell-rh/hypershell-stego/actions/runs/35460802241).
The core suite passed 315 top-level tests and 754 cases. Four declared live
checks were excluded from that suite. Hosted browser, console, and image jobs
passed. The separate API run `35460802217`, attempt 2, passed all 52 required
checks with no skip or failure. Four generation records matched, and cleanup
passed. The journal run `35460802209` passed all 32 required tests.

The main cluster browser run `35460802239`, attempt 2, stopped before it created
a Job. The installed Sandbox candidate policy differs from main. That result
is not a workflow pass. The policy was not changed for the test. The live Kata
check remains deferred because no suitable cluster is available.

The capacity candidate created 100 selected accounts through REST while the
fixture held 100 Gateway rows and 10,000 account clients. After adoption of
STEGO `58a3bcc`, complete account cleanup took 93.2722 seconds. It removed all
100 selected provider clients and users, closed their journals and rows, and
preserved the background state. The 30-second target remains unmet. All 33
journal tests passed, including the unchanged six-account restart test. Total
Gateway cleanup and larger production capacity remain unproved.
See [the capacity results and their limits](acceptance/provider-capacity.md).

The following control-account results apply to earlier source revisions. With
compiler `931f712`, the complete live browser workflow passed all 11 required
tests, and the core suite passed 738 cases. Account recovery and denied worker
writes passed. Independent browser cleanup found no test resources or held lease.
The separate API gate passed all 52 required tests, with matching source,
regeneration, and compiler records. Its cleanup also passed. See the
[current account installation record](acceptance/control-account-installation.md).

The following database results apply to earlier source revisions.
The external PostgreSQL change passed its hosted and complete public Gateway
workflows. The hosted core run at `29a238a` passed 311 top-level tests and three
generation checks. The live run at `62e82d5` passed all eleven required tests in
669.65 seconds, with 416 matching generated-file hashes, six complete browser
startup records, and independent cleanup. The nine retired CNPG test role
bindings were removed and their permission changes were checked. See the
[current database contract and evidence](acceptance/external-gateway-databases.md).
Production capacity and the other enterprise requirements remain open.

The following composition records apply to earlier source revisions.
The common registry and browser composition now have complete application
checks with compiler `00573709fb15a2a54de4242aa8fdbabee325179a`. The
[composition record](acceptance/common-browser-composition.md) identifies each
source and result. The full suite and public workflow passed at `af43205`.
The corrected API gate passed all 52 required tests at `442e7ff`. The CNPG
workflow passed all 11 required tests at `854bbb1` in 766.91 seconds.

Both deployed browser workflows verified all 415 generated-file hashes and 48
matching startup log/span pairs from six browser instances, with no failed
pairs. CNPG primary replacement preserved SQL identities, credentials, keys,
and data. The CNPG fixture needed one secondary Pod replacement for scheduling.
Independent runtime, volume, and Lease cleanup passed. These records retain
the limits of each check. Earlier [main workflow records](acceptance/main-cnpg-workflow-evidence.md)
remain available.

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
without assistance. The later workflows above include the generated
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
[complete public browser workflow](.github/workflows/jshell-browser.yml) for qualification.
Run heavy checks in CI or the restricted jshell environment. Use the saved
context explicitly, keep one live cluster test at a time, and verify cleanup
before the next test. Do not use Playwright or run performance tests on the
workstation. A missing result is not a pass.

The [acceptance index](acceptance/README.md) links each contract and earlier
results. The [previous overview](acceptance/repository-history-20260916.md)
preserves the implementation history. The full STEGO goal remains active.
