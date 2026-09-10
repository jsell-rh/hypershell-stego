The placement workflow creates a cluster, release, and managed database through
the generated application. It uses the returned IDs to create and retrieve a
Gateway. The test database starts with the schema and built-in roles. It has no
placement records.

The API serves create, get, list, patch, delete, and watch operations for all
three catalogs. REST shapes and protobuf descriptors use the pinned reference
contracts. Database namespaces use `openshell-db-` plus the hexadecimal form of
the first eight bytes of the KSUID payload. The result has 29 characters.
Namespace is absent from create and patch inputs. Database provider is immutable.

Platform admins and configured controllers can change catalog records. Gateway
creators can read and select them. Gateway owner and viewer grants do not give
catalog access. These checks use current verified claims and configured subjects.
This is a deliberate restriction from the reference fallback, which lets a
Gateway creator change catalog records. The user was asked to review this rule.
The more restrictive rule applies unless that decision changes.

Every catalog change and its event share one generated storage transaction.
Deletion checks live Gateway references in the same serializable transaction.
The concurrent test pauses deletion after its reference query, creates a Gateway,
and resumes deletion. PostgreSQL rejects the conflicting deletion for each of
the three catalog types. The Gateway keeps valid placement references.

The workflow checks both directions of REST and gRPC conversion, optional fields,
zero values, IDs, timestamps, count-only lists, search, denied writes, denied reads,
unauthenticated requests, watch changes, and Kafka delivery. It tests an offline
change followed by process restart. The exact retained event ID must reach Kafka.
The mutation failure test rejects outbox insertion and checks that catalog creation,
patches, and deletion all roll back. The migration test refuses missing placement
data, preserves IDs and timestamps after explicit data repair, and permits a
second application of the migration.

Watch clients must wait for response headers, list current state, and apply later
watch events. They must repeat this process after reconnect. Watches read current
committed records. They do not provide every historical version. Delete events
include the retained deleted record. Runtime token expiry and stream limits apply.
Events for different resource keys can arrive in either order. Kafka consumers
must handle duplicate delivery by message ID.

Apply `migrations/000006_placement_catalog.sql` before this API starts. Stop the old
API first. For an empty catalog, the migration supplies all fields and constraints.
For existing name-only rows, add the required columns and supply real data first:

```sql
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS provider varchar(64);
ALTER TABLE managed_clusters ADD COLUMN IF NOT EXISTS kubeconfig_secret varchar(253);
ALTER TABLE gateway_releases ADD COLUMN IF NOT EXISTS image varchar(2048);
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS provider text;
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS namespace varchar(29);
```

Set these fields for each existing row by its ID. Use actual cluster providers,
secret references, and release images. Managed database provider must be `cnpg`
or `deployment`. This includes retained deleted rows. Calculate each database
namespace with `go run ./cmd/catalog-namespace <database-ksuid>`. Do not change IDs,
creation times, or update times. Then run the migration. It fails and rolls back
if required data is missing or violates its constraints. The API does not run
this migration on startup.

STEGO generates numeric PostgreSQL checks for the declared 0–100 canary range.
The API also checks this range before storage. Inputs have bounded string lengths.
Secret fields contain references. The API does not accept namespace selection or
read secret contents. Patches leave omitted or null optional fields unchanged,
as in the reference pointer patch behavior.

This catalog workflow explicitly uses the CNPG placement path. It requires exactly
one live managed database with provider `cnpg`. A client database ID is a
compatibility placeholder and cannot choose placement. The default deployment
path now has a [separate workflow](deployment-placement.md). Catalog storage does
not prove cluster access, image availability, image
integrity, database provisioning, rollout behavior, or workload deployment.
Separate database and Gateway workload gates check cluster access, provisioning,
and workload behavior. Image integrity and rollout policy still need evidence.
[Field selection](field-selection.md) is supported. REST pages above 100 remain
open. The current secret references and image fields are bounded metadata; the
API does not dereference or execute them.

Run `scripts/check-gateway.sh` with PostgreSQL and Docker available. It verifies
dependencies, pinned regeneration, and the full race suite with Keycloak required.
Use `go test -race -count=1 ./acceptance -run 'TestPlacement|TestCatalog'` for the
focused workflow, migration, and concurrency checks. Set `STEGO_TEST_POSTGRES_DSN`
and `STEGO_REQUIRE_POSTGRES=1` for the focused command.

A local 100-call benchmark read a 20-row page from 10,001 matching cluster
records. It averaged 4.212 ms, 222,506 bytes, and 2,865 allocations per call on
Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H. It includes the domain
access rule, transaction, count, and page query. It excludes HTTP, TLS, token
verification, request role preparation, and concurrent load. This is a local
query measurement, not a production capacity result. Run
`go test -run '^$' -bench '^BenchmarkCatalogFilteredPage$' -benchtime=100x -benchmem ./acceptance`
with the PostgreSQL variables above to repeat it.

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package took 312.660 seconds. Dependency verification passed. The
compiler change passed hosted STEGO run `34310608422`. The pinned compiler is
`eae36e055bfcf26e564f40930ca52c7b8c0aeb06`.
