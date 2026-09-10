The CNPG provider creates the shared `openshell-db` Cluster in the namespace
derived from the ManagedDatabase ID. Set `DATABASE_PROVIDER=cnpg` on both the API
and `cmd/database-controller`. The deployment provider remains the default. An
unknown controller provider stops startup.

The provider uses STEGO's generated queue, watch and recovery scan, request
limits, conditional observation commits, cleanup summaries, API discovery
checks, and Kubernetes resource client. Hypershell supplies the CNPG resource definition, supported
settings, provider selection, readiness checks, and cleanup order. No additional
controller queue, retry loop, or local deletion cache was added.

Before an external change, the provider requires the exact `postgresql.cnpg.io/v1`
Cluster, Database, and DatabaseRole APIs. The tested operator is CNPG 1.30.0.
DatabaseRole was introduced in this release. API discovery does not grant access
to these resources. Each request still requires Kubernetes authorization.
See the [CNPG release notes](https://cloudnative-pg.io/docs/1.30/release_notes/v1.30/).

The initial profile has one instance, 1 GiB storage, CPU and memory limits, and
PostgreSQL 18.6 with a pinned image digest. It enables data checksums, disables
remote superuser access, and rejects unencrypted client connections. Encrypted
application connections are restricted to a database with the same name as the
role. An explicit rejection follows that rule. CNPG owns
its certificates, credentials, Pods, and volumes. The namespace requires the
restricted Pod security profile. This small profile is an acceptance target;
it does not provide high availability or a production capacity commitment.

Empty engine settings and `postgres` or `postgresql` select the tested engine.
The supported version values are empty, `18`, and `18.6`. Nonempty region,
instance class, and connection Secret overrides fail before external changes.
CNPG readiness updates only database status. It does not set the deployment
provider's shared connection Secret field. Each Gateway still needs its own
SQL database, role, credentials, and access policy on the shared Cluster.

The controller repairs the Cluster specification on every pass, including after
a ready observation. It checks one ready instance, a healthy Cluster phase,
the expected image, and a stable primary selection. It then reads that primary
Pod and requires the current Cluster owner UID, expected image and resource
limits, running database
container, and Pod readiness. These reads are observations, not a transaction
across Kubernetes resources. CNPG 1.30 does not populate the readiness condition's
`observedGeneration`. These checks cannot certify application of every Cluster
setting at a specific generation. See the [operator implementation](https://github.com/cloudnative-pg/cloudnative-pg/blob/v1.30.0/pkg/resources/status/transactions.go).

Deletion reads retained API state first. It checks namespace ownership, then
submits a Cluster deletion with UID and resource-version preconditions. It waits
for observed Cluster absence before it deletes the namespace with the same
preconditions. A pending or denied deletion keeps the cleanup obligation open.
The controller records completion only after the namespace is absent. Unsupported
mutable engine settings do not prevent cleanup of a valid stored placement.

Use the [CNPG RBAC manifest](../deploy/cnpg-database-controller-rbac.yaml) for a
separate provider service account. It needs no direct Secret access. Give its
verified API subject the `ManagedDatabase` / `observe.provider` grant with target
`cnpg`, plus the `ManagedDatabase` / `provider` cleanup grant. Use the API and
Kubernetes TLS and private token file settings from the
[database workflow](database-workflow.md). CNPG mode does not require a
cert-manager ClusterIssuer setting. The test fixture uses cert-manager for its
existing shared test setup.

Run `scripts/check-workload.sh cnpg` on Linux amd64 with Docker and
`STEGO_TEST_POSTGRES_DSN`. The script creates an isolated kind cluster and installs
CNPG from a checksummed release manifest with a pinned operator image. The
`cnpg-database-workflow` CI job also checks regeneration from the compiler pin.

The workflow creates the catalog record through REST, verifies event delivery,
reads readiness through generated gRPC, and creates a Gateway that selects the
shared record. A live Gateway prevents database deletion. SQL checks require
certificate and hostname verification, a restricted application role, and a
failed unencrypted connection. A stored marker and credentials must survive
controller and database Pod restart. A changed Cluster setting must be repaired
after the controller restarts. Repeated stable passes must preserve the Cluster
specification generation.

The deletion test stops the controller, deletes the Gateway and database, and
restarts the API. A forbidden Cluster deletion must preserve the namespace and
pending cleanup count. After authorization is restored, recovery must remove
the Cluster and namespace and commit a zero pending count. Unit checks cover
missing or partial APIs, unsupported settings, foreign ownership, delete
preconditions, provider selection, and stale or unready Pods.

Gateway execution on CNPG, per-Gateway SQL resources, SQL drift repair, backups,
restore, upgrades, network policy, reference namespace migration, database desired
generations, placement history, and cross-process fencing remain open. The test
uses the Kafka protocol fixture. It does not establish production event delivery,
recovery time, or database capacity.

The first complete local workflow passed with race detection in 73.43 seconds
on 2026-09-10. Five stable provider passes took 48.8 ms. This small measurement
includes Kubernetes HTTPS requests and excludes provisioning, SQL queries, API
event delivery, and concurrent load. It does not establish production capacity.

The deployment database and five related regression tests passed after the
compiler update in 102.420 seconds with race detection. These checks include
retained replay under two database collations, stale observation rejection,
provider write grants, cleanup transactions, and real deployment deletion.

The complete fresh-cluster script passed with compiler
`934bc01a0f199ae64001e9ccb0c9e80fd0ae96a4`. The CNPG workflow took 83.19 seconds
under race detection; the acceptance package took 84.247 seconds. Five stable
passes through generated discovery took 48.7 ms. The script verified its pinned
tools and manifests and removed its test cluster. Provider unit tests and
`go vet` also passed.

The connection-isolation regression failed against `d01bb29` with the old
profile: the application role connected to the existing `postgres` database.
The baseline failed in 121.64 seconds. The revised profile passed the complete
fresh-cluster workflow in 85.54 seconds under race detection; the package took
86.591 seconds. The test checks the forbidden connection before and after
restart, with successful encrypted application queries in both phases. Five
stable provider passes took 52.1 ms. This is a connection access check; it does
not certify isolation of all PostgreSQL catalog metadata or production capacity.
