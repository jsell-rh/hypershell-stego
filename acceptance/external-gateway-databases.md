# Gateway database contract

The user selected the database model in
[upstream PR #300](https://github.com/openshift-online/hypershell/pull/300) on
2026-09-17. The reviewed PR was open at commit
`9ebf4d77ebc7532327e1d7ae79806366542b9800`.
This decision replaces the earlier requirement for a Hypershell CNPG path.

## Ownership

The operator supplies a PostgreSQL server and its admin credential before
the Gateway controller starts. Terraform can supply RDS. Hypershell does not
create the server, select a database provider, install a database operator, or
manage server storage. Keep the server near its Gateways and their controller.

Each controller installation uses one configured PostgreSQL endpoint. A server
can hold separate logical databases for the API, identity provider, and
Gateways. The operator can also supply separate servers for these components.
Gateway creation always creates a separate logical database and restricted
login. The API has no database inventory or `database_id`.

The controller reads its mounted `database.json` file for each operation.
The file contains `host`, `port`, `database`, `user`, `password`, and `ca`.
Admin password changes do not require a controller restart. The Gateway gets
only its own credential and the server CA. All Gateway and provisioner SQL
connections verify the certificate and hostname. They have no plaintext or
unverified TLS fallback.

STEGO provides SQL names, credential preparation, object ownership checks,
database and role creation, restricted access, deletion, and telemetry.
Hypershell supplies the Gateway identity, placement, and durable state policy.
The operator must restrict access to all other logical databases on a shared
server. Removing `PUBLIC` access only from a new Gateway database is not enough
to prevent that Gateway login from entering another database.

The implementation keeps the generated owner role and provisioning ledger.
It does not copy the upstream SQL client or change existing database names.
It also keeps durable asynchronous deletion: DELETE returns 202, and the
controller records progress and retries failed cleanup across restarts.
Deletion must preserve the server and unrelated component data.

## Test changes

The complete public Gateway workflow uses the existing bounded PostgreSQL
container as an operator-supplied server. It exercises verified TLS, separate
Gateway credentials, REST and gRPC access, events, server process restart,
controller recovery, durable deletion, and regeneration. The fixture starts
before the Gateway controllers and supplies a non-superuser provisioner.
Its restart test does not prove RDS or multi-node database failover.

The CNPG installer, operator permissions, private credential projection,
network-rule additions, and CI jobs are removed. Historical migrations and
test records remain unchanged. This change does not delete a deployed server
or migrate existing data. A server endpoint change still requires an explicit
data migration; reconciliation must not silently create an empty replacement.

The external-only source passed its hosted checks and complete PostgreSQL
workflow. Earlier CNPG results remain evidence for their recorded source only.
Production capacity, RDS failover, and the deferred live Kata test remain open.

## Hosted evidence

At source `29a238a`, the hosted browser workflow passed in 97.62 seconds. It
checked login, Gateway creation, grants, REST and gRPC access, event delivery,
session recovery, and account creation and deletion. Three browser instances
each supplied correlated startup logs, traces, and metrics for all eight stages.
The two screenshots were reviewed. The fixture does not run a real Gateway.

The same run passed 231 UI tests, three generation checks, and builds and
entrypoint/user checks for seven generated images. The core job also passed:
311 top-level tests, 670 total test cases, three generation checks, and hosted
container cleanup. Four named tests have separate live workflows.
The complete external PostgreSQL cluster workflow passed at `62e82d5`
in [run 35232271576](https://github.com/jsell-rh/hypershell-stego/actions/runs/35232271576).
All eleven required tests passed. The main workflow took 669.65 seconds. Checks
matched all 1,427 source files and 416 generated-file hashes, published compiler
bytes, the deployed console image, and the live provisioner recovery record.
See the [hosted evidence](external-gateway-hosted-evidence.json) and
[complete live evidence](external-gateway-live-evidence.json).

The live workflow covered Gateway creation, owner grants, filtered and denied
REST and gRPC access, events, account operations, the upstream dashboard,
PostgreSQL process restart, namespace replacement, provisioner recovery, and
durable deletion. Six browser instances each exported all eight startup stages
with matching logs, traces, and metrics. SQL telemetry included preparation,
failed cleanup, recovery, and successful deletion. The three screenshots were
reviewed. Independent cleanup found no remaining test workloads, fixtures,
allocated namespaces, or held test lease. No scheduling change was required.
These results qualify this application change, not the full enterprise goal.

## Test installation policy

CI rendered the new installation policy without cluster access. Independent
checks matched all 1,427 source files, the published compiler records, and the
19 generated cluster resources. Only one network validation expression changed:
it no longer permits the old CNPG destination.

After the previous workflow and independent cleanup passed, the operator
applied that exact expression with object identity and version checks. A server
dry run and policy type check passed. The immutable installation record was
replaced under the test lease. Readback confirmed the planned rules and the
unchanged policy identity. No test workload was active during this update.
See the [policy evidence](external-gateway-policy-evidence.json).

The default regeneration path also passed after the CI workflow change. All
415 generated files matched the committed output, with an empty change patch.
The [last retired CNPG result](retired-cnpg-final.md) remains available separately.

The preceding namespace-allocation change passed 311 top-level core tests,
670 total test cases, three generation checks, and the hosted browser workflow.
It removed unused handwritten RBAC declarations and made the allocator check
explicit before Kubernetes writes. These results apply to `04e8e70`, before
the external-only fixture change. See the
[allocation evidence](namespace-allocation-authority-evidence.json).

## Retired test access

A read-only cluster check found nine role bindings from the old CNPG test
installation. Their object identities, subjects, and role references match the
installation record. This includes access for the CI runner, old operator,
database service account, and application test fixture.

The [removal plan](retired-cnpg-access-plan.json) records those exact bindings.
After the external workflow and independent cleanup passed, all nine bindings
were removed under the test lease with object identity and version checks.
Independent checks confirmed their absence, five denied old permissions, and
two retained permissions for the current test. Shared CRDs, namespaces, roles,
and unrelated workloads were unchanged. See the
[removal evidence](retired-cnpg-access-evidence.json).
