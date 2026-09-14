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
  --cluster-id CLUSTER_ID --engine postgresql --engine-version 18
```

Use the returned managed-cluster ID for `CLUSTER_ID` when creating the database.
A Gateway creator can retrieve the IDs and create a Gateway:

```sh
hsctl list managedClusters --size 20
hsctl list gatewayReleases --size 20
hsctl list managedDatabases --size 20
hsctl create gateway --name example --cluster-id CLUSTER_ID \
  --release-id RELEASE_ID --database-id DATABASE_ID
```

The API selects the sole live database server of the configured provider in the
Gateway's managed cluster. The default provider is CNPG. External PostgreSQL is
also a supported catalog provider; its SQL provisioning remains open. A missing
or ambiguous local server prevents Gateway creation. The client database ID is
a compatibility placeholder and cannot override selection. New deployment-backed
database records are rejected. Use `get managedDatabase ID` to inspect the
returned server record. The server assigns its namespace. Secret fields hold
references. These commands do not create a Kubernetes cluster or upload a Secret.

Catalog creation requires a platform administrator. Controllers have separate,
scoped observation and cleanup permissions. Gateway creators can read and select
records. A Gateway owner grant alone does not give catalog access. See
[placement catalogs](placement-catalog.md) and
[database providers](database-providers.md).

Database deletion waits for Gateway workload cleanup. Managed-cluster deletion
also waits for database provider cleanup. HTTP 409 is not successful deletion;
wait for the controllers and retry. Release deletion checks live Gateway links.
The CLI cannot supply controller cleanup observations on behalf of its user.

`TestGeneratedCLICatalogWorkflow` starts with empty placement catalogs. It uses
PostgreSQL, signed identities, verified TLS, a Kafka protocol fixture, and
separate generated CLI and API processes. It verifies the following:

- Catalog IDs, API shapes, timestamps, and the generated database namespace.
- Reference command names, aliases, filtered pages, and access rules.
- A zero canary percentage and explicit null optional values.
- Rejection of invalid percentages, integer overflow, and a chosen namespace.
- Rollback of catalog creation when its event cannot be stored.
- Gateway creation using the same local server under explicit and default CNPG modes.
- Agreement between CLI and gRPC reads after API restart.
- Refusal to delete a catalog while a live Gateway uses it or required cleanup is pending.
- Event delivery, confirmed deletion, and removal from subsequent lists.

The earlier deployment-based fixture no longer matches the supported providers.
The current check uses controlled cleanup observations through authenticated TLS
gRPC. Actual CNPG effects are covered by the
[live browser workflow](browser-gateway-workload.md).

On 2026-09-14, bounded jshell Job `stego-placement-1a725083/check` passed the
catalog CLI workflow in 15.34 seconds and the apply workflow in 10.54 seconds.
The API parent-cleanup regression check and all CLI field-contract checks also
passed with the race detector. The old CLI declaration failed the field check
because it omitted the required database cluster ID.

Both generation passes and post-test output matched all 230 generated and build
record hashes. The tested CLI source matched the checkout. The two CLI documents
and a separately tested concurrency fixture changed after the source snapshot.
The Job completed, and its namespace and private launch files were removed.
Evidence is in `/tmp/hypershell-cli-placement-ximht64v`. The compiler pin is
`2f3a2c06bff4a0a6811757e9a168eb810f57ce53`. Full Hypershell CI still has other
outdated fixtures; this result does not establish a full suite pass.
