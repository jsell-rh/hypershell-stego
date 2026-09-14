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
