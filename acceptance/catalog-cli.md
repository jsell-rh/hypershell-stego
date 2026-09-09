The generated CLI can create, retrieve, list, and delete managed clusters,
Gateway releases, and managed databases. Hypershell supplies command names,
paths, and fields. The existing STEGO command runtime executes them. This step
requires no compiler or generated source change.

The reference names `managedCluster`, `gatewayRelease`, and `managedDatabase`
are accepted. The aliases `managed-cluster`, `gateway-release`, and
`managed-database` are also accepted. Get and list accept singular and plural
names. Create and delete accept singular names. Creation supports field flags
or `--body FILE`. Deletion requires `--yes`. Lists return one bounded JSON page.
Reference table output, automatic pagination, and interactive prompts remain
open. The [CLI port table](cli-port.md) records the wider remaining scope.

A platform administrator can prepare placement records:

```sh
hsctl create managedCluster --name primary --provider kubernetes \
  --kubeconfig-secret cluster-access --region east
hsctl create gatewayRelease --name stable \
  --image registry.example/gateway:v1 --canary-percent 0
hsctl create managedDatabase --name shared --provider cnpg \
  --connection-secret database-access
```

A Gateway creator can retrieve the IDs and create a Gateway:

```sh
hsctl list managedClusters --size 20
hsctl list gatewayReleases --size 20
hsctl list managedDatabases --size 20
hsctl create gateway --name example --cluster-id CLUSTER_ID \
  --release-id RELEASE_ID --database-id DATABASE_ID
```

The API selects database placement. Under CNPG, it uses the sole live CNPG
catalog record. Under the default deployment mode, it creates a dedicated
database and returns that database ID. The client database ID is a compatibility
placeholder. It cannot override either selection. Use `get managedDatabase ID`
to inspect the returned record. The server assigns its namespace. Secret fields
hold references, not secret contents. These commands do not provision a cluster
or load a secret.

The current API policy permits platform administrators and configured
controllers to change catalogs. Gateway creators can read and select records.
A Gateway owner grant alone does not give catalog access. This is the existing
policy described in [placement catalogs](placement-catalog.md).

`TestGeneratedCLICatalogWorkflow` starts with empty placement catalogs. It uses
PostgreSQL, signed identities, verified TLS, a Kafka protocol fixture, and
separate generated CLI and API processes. It verifies the following:

- Catalog IDs, API shapes, timestamps, and the generated database namespace.
- Reference command names, aliases, filtered pages, and access rules.
- A zero canary percentage and explicit null optional values.
- Rejection of invalid percentages, integer overflow, and a chosen namespace.
- Rollback of catalog creation when its event cannot be stored.
- Gateway creation using catalog IDs under CNPG and default deployment modes.
- Agreement between CLI and gRPC reads after API restart.
- Refusal to delete a catalog while a live Gateway uses it.
- Event delivery, confirmed deletion, and removal from subsequent lists.

The focused race workflow passed in 8.10 seconds. Request-field checks passed
for all catalog commands. They compare fields and types with the application
request structs. This duration includes setup and is not a capacity claim.
The compiler pin remains `dc2af4e283d0da07a17bdefd2acf0b97a6a2dd2f`.
The hosted checks run the full acceptance and workload suites.

Pinned regeneration and static checks passed before commit. No generated source
or dependency change was needed. The focused workflow checks the changed CLI
behavior locally. CI runs the complete application and workload gates.
