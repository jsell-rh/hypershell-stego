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

The new source requires hosted checks and a complete external PostgreSQL
workflow result before qualification. Earlier CNPG results remain evidence
for their recorded source only.
