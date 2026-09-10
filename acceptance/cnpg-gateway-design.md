The next CNPG gate must run a real Gateway against the shared database. It must
cover two Gateway identities, separate SQL roles and databases, encrypted
connections, access denial, stored encrypted data, restart, drift repair, and
offline cleanup. The existing managed-database gate does not prove this result.

The reference creates one CNPG `DatabaseRole`, `Database`, and password Secret
per Gateway. It uses a shared Cluster named `openshell-db`. The variant can reuse
STEGO's generated runtime, API discovery, Kubernetes ownership checks, and
conditional cleanup commits. Resource definitions and Gateway access policy
remain in Hypershell.

The working design uses inline managed roles because CNPG can compare and repair
them when its Cluster reconciles. The user was asked about this choice and can still change
it. SQL absence evidence is required for cleanup; role status alone is not
sufficient. The implementation and live test are described in
[the Gateway workflow record](cnpg-gateway.md).

CNPG 1.30 applies a `DatabaseRole` when its specification or password Secret
changes. It does not automatically repair direct `ALTER ROLE` changes. Repeated
GET requests or unchanged resource writes do not add this guarantee. The
`DatabaseRole` API does provide generation-aware status and deletion finalizers.
See [CNPG role management](https://cloudnative-pg.io/docs/1.30/declarative_role_management/).

The live gate exposed a timing gap in that contract. A direct SQL privilege
change remained for 150 seconds while the Cluster still reported the role as
reconciled. The controller now checks authentication and SQL role attributes
on each pass. A confirmed mismatch requests a CNPG reload through the generated
Kubernetes client. A successful check does not write a reload request.

Inline roles have a separate limitation. Their status groups do not identify
the specification generation. The same `reconciled` group can describe a
present role or a confirmed absent role. An old success can therefore remain
visible after the desired role changes to `ensure: absent`. Do not use that
status alone to commit cleanup completion. A direct, bounded, read-only SQL
catalog check supplies absence evidence. It does not need a superuser password.
The workflow record defines routing, TLS identity, credentials, and limits.
See the [operator status implementation](https://github.com/cloudnative-pg/cloudnative-pg/blob/v1.30.0/internal/management/controller/roles/reconciler.go).

The shared database profile must deny connections to other databases. PostgreSQL
can enforce this with a `hostssl sameuser` rule, followed by an explicit rejection
of other client connections. The explicit rejection is required because CNPG
adds a default client rule after the supplied rules. The Gateway SQL database
and role must have the same name. Operator replication and certificate rules
remain ahead of these client rules. The database gate checks a valid connection,
then attempts an encrypted connection to the existing `postgres` database with
the same application credentials.

Gateway keys also need separate durable identities. The old deployment key
store binds its database namespace to one Gateway. The CNPG key path uses a
separate immutable Secret and ConfigMap for each Gateway in the shared namespace.
The ConfigMap stores a fingerprint of the four validated key fields. Material
is returned only after the fingerprint exists. A denied fingerprint write can
resume with the original Secret; it must not generate different keys.

Source names encode every byte of the canonical Gateway KSUID as hexadecimal.
Lowercasing a KSUID can merge distinct IDs, so the reference's names produced by lowercasing IDs
are not used. Owner labels include the complete Gateway and database IDs.
The shared namespace keeps its database owner and is not linked to one Gateway.
Lost or changed source material fails closed. If both source records are absent
but the Gateway's SQL Database resource exists, new keys are forbidden. Before
new keys are created, a SQL query must also confirm absence of the role and
database. This check covers SQL state left after loss of the Kubernetes records.

Race tests cover two concurrent Gateways in one namespace, concurrent writers
for the same Gateway, stable keys through
client restart, interrupted fingerprint creation, lost keys, changed keys,
foreign ownership, and distinct canonical IDs that differ only by letter case.
These tests isolate the key protocol with a TLS Kubernetes API fixture and
explicit SQL-check results. They do not prove live SQL behavior. The live
Gateway gate tests the complete CNPG path, including actual SQL checks, retained
keys, application calls, and cleanup. Use its recorded result to assess delivery.

Deletion must stop the Gateway workload before it removes its SQL database.
It must confirm database and role removal before it removes retained credentials
and key identity. A shared database namespace must survive one Gateway's cleanup.
Recovery must retain enough placement and ownership evidence to repeat cleanup
after restart and after a recorded completion. Direct SQL access errors,
operator failure, stale status, or missing cleanup identity must not count as
successful cleanup.
