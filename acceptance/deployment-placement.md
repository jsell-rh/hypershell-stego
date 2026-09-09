The generated application now uses the reference deployment placement path by
default. Each Gateway creation makes a new ManagedDatabase with provider
`deployment`. The database name is `gw-<gateway-name>-db`. Its namespace comes from
its own KSUID. A requested database ID cannot select another Gateway's database.

`DATABASE_PROVIDER` accepts `deployment` and `cnpg`. An unset or empty value selects
`deployment`. Every other value stops application startup. The domain constructor
uses the same default and validation. Existing installations that use one shared
CNPG database must set `DATABASE_PROVIDER=cnpg` to keep that behavior. CNPG mode
requires exactly one live managed database with provider `cnpg`. It does not select
from an ambiguous catalog or silently change provider.

The REST create body must contain `database_id`. An empty string is valid and is
the normal placement placeholder. Missing and null values are invalid. Other
string values are ignored. The common STEGO JSON reader enforces presence with
`stego:"required"`; it does not confuse a required property with a nonzero value.
The protobuf create field keeps its reference behavior. A missing protobuf string
has the empty value and does not control placement. Both REST and gRPC patches
continue to ignore a requested database ID.

Gateway authorization applies before placement. A Gateway creator does not need
or receive catalog write access. In deployment mode, one serializable transaction
commits the database, Gateway, owner grant, and three resource events. A failure
at any write or event step rolls back that transaction. Request identity and
global-role preparation remain a separate transaction. Thus a rejected request
can still update the caller's own verified identity or global-role projection.

The new application workflow starts with no managed databases. It creates the
cluster and release through REST, creates a Gateway through REST, and reads it
through gRPC. A second creator supplies the first Gateway's database ID through
gRPC and receives a different database. The test checks filtered Gateway lists,
owner grants, denied requests, immutable placement, database and Gateway watches,
and all three Kafka events. A stopped application then commits another creation
through the domain service. After restart, all three retained message IDs must
reach Kafka. The real Keycloak browser login and sharing workflow also uses the
default deployment path and checks its database record.

The fault test rejects database insertion, Gateway insertion, owner insertion,
and each of the three event kinds. Each case must leave no partial creation.
The concurrency test creates eight Gateways for one user and requires eight
separate deployment databases. It checks the total resources, grants, and events
after retries of explicit transaction conflicts. The startup test runs the generated
binary with an unknown provider and requires it to exit with a configuration error.
Existing shared-database workflows now select CNPG explicitly.

Apply `migrations/000007_deployment_database_names.sql` after migration 000006 and
before starting this API. The new migration widens database names to 261 characters.
The API uses a 261-byte limit. This holds a valid 255-byte Gateway name plus the
six-byte database name prefix and suffix. The migration preserves existing IDs,
names, namespaces, and timestamps, and can run again. A test creates a Gateway
with a 255-byte name and checks its complete database name. The API does not run
schema migrations at startup.

Run `scripts/check-gateway.sh` with PostgreSQL and Docker available for the full
gate. The focused command is:

```sh
STEGO_REQUIRE_POSTGRES=1 STEGO_REQUIRE_KEYCLOAK=1 go test -race -count=1 ./acceptance \
  -run 'TestDeployment|TestGeneratedRuntimeRejectsUnknownDatabaseProvider|TestPlacement|TestCatalog|TestGatewayUserLoginFollowsStoredGrants'
```

Set `STEGO_TEST_POSTGRES_DSN` to the test PostgreSQL connection before either command.

This workflow creates durable placement records. It does not create a PostgreSQL
pod, resolve cluster credentials, or deploy a Gateway workload. Gateway deletion
does not automatically remove its ManagedDatabase. The catalog rejects database
deletion while a live Gateway refers to it. An authorized operator can remove the
database record after Gateway deletion. The next control-plane workflow must
provision workloads and recover cleanup after restart. ManagedDatabase deletion
replay, its reference capability header, and durable cleanup retries remain open.

A local 100-call benchmark averaged 3.088 ms, 116,992 bytes, and 1,651 allocations
per creation on Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H. It
includes the database, Gateway, owner grant, and three queued events in one
transaction. The caller identity already exists. The measurement excludes HTTP,
TLS, token verification, role preparation, Kafka delivery, Kubernetes work, and
concurrent load. It is not a production capacity result. Run
`go test -run '^$' -bench '^BenchmarkDeploymentPlacement$' -benchtime=100x -benchmem ./acceptance`
with the PostgreSQL variables above to repeat it.

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package took 318.581 seconds. Dependency verification and pinned
generation passed. The compiler revision is
`35349fea6a2b112ac53a59f7cf649550b12369a3`, which passed hosted STEGO run
`34311390396`. The new workflow also passed its focused check in 5.125 seconds.

The placement record now drives the [database workload workflow](database-workflow.md).
That test uses an isolated Kubernetes cluster and verifies persistent data,
TLS, restricted database privileges, and cleanup after disconnected deletion.
Gateway workload deployment remains open.
