# Database providers and locality

The user changed the database requirement on 2026-09-14. Deployment-backed
PostgreSQL is no longer a supported target. The replacement must support CNPG
and external PostgreSQL. This decision supersedes the earlier deployment
placement requirement; the code removal and replacement checks are not complete.

## Locality

Place each Gateway's database with that Gateway. For CNPG, use the Gateway's
managed Kubernetes cluster. Run the database reconciler in that cluster. The
central API does not need to run there.

For external PostgreSQL, the operator must register a server local to the
Gateway's managed cluster. Record that cluster association explicitly. A DNS
name or an API event is not proof of placement. The reconciler must run beside
the database it manages. Do not select a remote server merely because it is the
only database catalog entry in the control plane.

## One logical database per Gateway

Both providers must create one logical database and one login for each Gateway.
The user explicitly requires external PostgreSQL reconciliation to create the
database, user, permissions, and credentials. External mode is not limited to
connections that the operator has already created.

Each login must be able to use only its Gateway's database. Prove successful
access to its own database and denied access to another Gateway's database and
unrelated databases. Use verified TLS and bounded operations. Keep passwords
out of API responses, logs, traces, metrics, and test evidence.

Creation, retries, restart, and cleanup must check current ownership and stored
placement. A missing credential must not silently replace a credential for
existing data. Do not convert or delete old deployment-backed database records
or volumes as part of a schema upgrade.

## Responsibility and evidence

STEGO must supply reusable SQL provisioning, connection security, bounded
execution, controller behavior, telemetry, and Kubernetes allocation. Hypershell
supplies Gateway identity, catalog selection, locality, and lifecycle rules.

Replace the deployment-backed application fixture with the supported providers.
Retain the Gateway creation, atomic owner grant, REST and gRPC access, filtered
lists, denied requests, event delivery, restart, and regeneration checks. Add
database isolation checks with actual PostgreSQL connections. CNPG allocation
must preserve the shared-cluster namespace isolation requirement.

## Database registration and selection checks

The first implementation step requires `cluster_id` when an operator registers
a CNPG or external PostgreSQL server. Gateway creation selects one server in
that cluster. A missing or ambiguous server prevents creation. A Gateway cannot
move to another cluster while it retains its database association. Existing
deployment-backed records remain in storage; new registrations are rejected.

On 2026-09-14, the bounded jshell Job `stego-placement-2478d539/check` passed:

- Catalog, database placement, and Gateway unit checks with the race detector.
- Local CNPG and external server selection, rejected remote selection, and
  rollback after an ambiguous selection.
- REST and gRPC registration, denied requests, and placement after API restart.
- Repeated migration with preserved legacy rows.
- Existing protobuf fields and the three explicit database placement additions.
- The operator-set default release and atomic creation checks.

Two generation runs and the checks produced identical hashes for all 230
generated files and build records. The frozen source matched 812 checkout files.
The Job completed. Its namespace and local private launch files were removed.
Local evidence is in `/tmp/hypershell-local-database-final-a52ya99b`.

This gate covers registration and selection. It does not prove SQL provisioning,
physical network locality, cross-database access denial, or the complete Gateway
workflow. Deployment runtime removal and conversion of the older application
fixtures remain open. The cleanup summary also needs a check that combines
provider and cluster filters. Generated SDK type names need review because the
new OpenAPI source paths changed some names. Keep this work on the working
branch until the supported-provider workflow passes.
