The database workflow connects Gateway creation to a real PostgreSQL workload.
`TestDatabaseWorkloadAndOfflineDeletion` creates a Gateway through REST. The API
commits its private ManagedDatabase, Gateway, owner grant, and events. A separate
controller uses generated gRPC and HTTPS clients to read the resulting state,
create Kubernetes resources, and publish database readiness through gRPC.
Hypershell placement rules remain outside generated output.

The test writes data through the database Service with certificate and hostname
verification. It checks that an unencrypted connection fails. The application
role has no superuser, role creation, database creation, replication, or row policy
bypass privileges. Data and the application password survive Pod and controller
restart. Five repeated reconciliations must leave the Deployment unchanged.

The test then stops the controller, deletes the Gateway and database through REST,
and restarts the API. It first denies the controller's Kubernetes delete request.
The namespace must remain, and the failure must be reported. After access is
restored, historical replay must remove the namespace. A namespace with another
owner must survive both provisioning and deletion attempts.

`TestDatabaseDeleteReplayThroughGeneratedRuntime` checks the reference gRPC
extension. Live watches send `hypershell-managed-database-delete-tombstones: v1`.
A request with `hypershell-managed-database-replay: deleted-v1` returns deleted
records and then closes. Only configured controller subjects can use replay.
Ordinary users, Gateway creators, and platform administrators are denied. Invalid
replay modes are rejected. The test crosses a 100-record replay page and checks
IDs, order, provider, namespace, completeness, and exclusion of live rows.

Replay now uses STEGO's cursor scanner for pagination, complete-page checks,
request deadlines, and a page-count limit. Hypershell supplies the authorized
deleted-state query and the reference event shape. The scanner preserves database
order. A Go string comparison cannot validate an opaque database cursor.

The replay test runs with C and ICU collations. Two fixed IDs ensure that ICU
ordering differs from Go string ordering. Expected results come from an ordered
database query. Each case repeats after API restart, verifies denied requests,
and checks an empty authorized replay. Denied requests must not confirm the
replay capability. The old replay loop failed the ICU case with an internal
error; the generated scanner passed both collations.

STEGO postgres-adapter 3.4.0 adds `ListOptions.OnlyDeleted`. It filters deleted
root records before counting and paging. Related access records must still be
live. A separate generated Record service checks this behavior with PostgreSQL.
The variant uses a validated KSUID cursor for replay. No Hypershell type or
permission rule was added to the compiler.

Run `scripts/check-database-workflow.sh` on Linux amd64 with Docker. Set
`STEGO_TEST_POSTGRES_DSN` to a PostgreSQL connection that can create test databases.
The script verifies tool and manifest checksums, creates its own kind cluster,
installs cert-manager with pinned image digests, runs the race tests, and removes
the cluster. It never selects the current kubeconfig context. The CI
`database-workflow` job also checks pinned regeneration. The full Gateway job
continues to require PostgreSQL and Keycloak.

For an existing isolated test cluster, set `STEGO_TEST_KUBECONFIG` to its config
file. Its context must start with `kind-stego-`, and cert-manager must be ready.
Set `STEGO_REQUIRE_KUBERNETES=1` to make a missing cluster a test failure. Run
`go test -race -count=1 -v ./acceptance -run '^TestDatabase'`.

The local run on 2026-09-09 used Go 1.26.8, PostgreSQL 18.6, kind 0.33.0,
Kubernetes 1.35.8, and cert-manager 1.21.1. The workload test with a limited
application role and a forced delete denial passed in 60.20 seconds. The separate
replay test passed in 3.05 seconds. Five stable reconciliations took 75.7 ms in
one run on an Intel Core Ultra 9 185H. This small measurement includes Kubernetes
HTTPS requests and excludes provisioning, database queries, API event delivery,
and concurrent load. It does not establish production capacity.

The complete cluster setup script passed a separate run. The workload took
70.93 seconds and replay took 3.47 seconds; the acceptance package took
75.443 seconds. The later SQL readiness workflow passed in 60.02 seconds, with a 61.073-second
acceptance package run. Controller failure tests and `go vet` also passed.

The controller command is `go run ./cmd/database-controller`. Its settings are:

| Variable | Required value |
| --- | --- |
| `HYPERSHELL_API_GRPC_ADDR` | Hypershell gRPC host and port |
| `HYPERSHELL_API_CA_FILE` | Hypershell CA file |
| `HYPERSHELL_API_TOKEN_FILE` | Private file with a token for an allowed controller subject |
| `HYPERSHELL_KUBERNETES_URL` | Kubernetes HTTPS API origin |
| `HYPERSHELL_KUBERNETES_CA_FILE` | Kubernetes CA file |
| `HYPERSHELL_KUBERNETES_TOKEN_FILE` | Private projected service-account token file |
| `HYPERSHELL_DATABASE_CLUSTER_ISSUER` | Configured cert-manager ClusterIssuer name |

Token files must have no group or other access. The generated clients read them
again for each request. Project Kubernetes tokens with mode `0400` and arrange
for the controller process to read them. The controller does not create permanent
service-account token Secrets. Apply the [RBAC manifest](../deploy/database-controller-rbac.yaml)
after creating the `hypershell-control-plane` namespace. These permissions belong
to a trusted platform controller. They include namespace creation and deletion
and access to database Secrets across namespaces.

The configured issuer must supply a valid server certificate and `ca.crt`.
The controller verifies the certificate chain, expiry, usage, and Service DNS
name. cert-manager owns issuance and renewal. A certificate change updates the
Pod template hash so the next reconciliation replaces the database Pod. The
initial issuance path has a real test; scheduled renewal and CA rotation do not
yet have an acceptance test. The issuer choice remains subject to user review.
See the [cert-manager Certificate contract](https://cert-manager.io/docs/usage/certificate/).

Each deployment database gets its own namespace, PVC, Service, ConfigMap,
Certificate, TLS Secret, application credential Secret, bootstrap credential
Secret, and Deployment. The PostgreSQL 18.6 Alpine image is pinned by digest.
Provisioning accepts an empty engine or `postgres`/`postgresql`, and an empty
version or `18`/`18.6`. Region, instance class, and external connection Secret
overrides are rejected before Kubernetes writes. Cleanup still uses the stored
identity if these mutable settings are invalid.
The Pod runs as UID and GID 70 with a read-only root filesystem, no added Linux
capabilities, no privilege escalation, no mounted service-account token, and
RuntimeDefault seccomp. Readiness requires a SQL query with certificate and
hostname verification. Failed reconciliation changes a stored ready state to
provisioning or error through the API. Requests are 100 millicores and 256 MiB; limits are
500 millicores and 512 MiB. The PVC requests 1 GiB. These values are a tested
starting point, not production sizing.

`openshell-db-credentials` contains `host`, `port`, `dbname`, `user`, `password`,
`uri`, `sslmode`, and `ca.crt`. Clients must use `verify-full` and the supplied CA.
The URI does not contain a local CA file path. The separate bootstrap Secret must
not be copied into Gateway workloads. Missing credentials for an existing PVC
cause an error. The controller does not generate replacement passwords for an
existing database.

The controller starts and drains a live watch before replay and list scans. It
uses one worker and a bounded queue. Each scan repeats after ten seconds. A
failed deletion stays in the stored replay data. Delete requests include the
observed namespace UID and resource version. Missing namespaces count as cleaned
up; conflicts and denied requests remain failures. The controller validates the
stored namespace against its database ID and checks ownership before mutations.
See [Kubernetes API concurrency rules](https://kubernetes.io/docs/reference/using-api/api-concepts/).

This result covers deployment databases on one configured Kubernetes cluster.
It does not implement CNPG provisioning, backups, restore, database upgrades,
OpenShift UID allocation, Gateway workload deployment, network policy enforcement,
or multiple controller coordination. Migration from namespaces owned by the
reference controller also remains open. It does not establish a production
availability or capacity target. List pages contain 20 records to stay inside
the generated RPC response limit. Replay pages contain 100 stored rows but send
one record per message. Large-history completion within the generated five-minute
stream lifetime still needs capacity testing. Production Kafka remains outside
this test; the API uses the TLS Kafka protocol fixture.

A dependency review before publication found reachable
[GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970). The final module uses
`golang.org/x/text` 0.40.0, `golang.org/x/net` 0.56.0, `golang.org/x/sys` 0.48.0,
and `filippo.io/edwards25519` 1.1.1. The latter updates remove findings outside
the current call graph as well. STEGO supplies minimum versions for the affected
runtime dependencies and preserves higher application versions. Govulncheck 1.4.0
reports no known vulnerabilities in the final module set. CI now requires this
scan. Static analysis and a vulnerability database do not prove the absence of
unknown defects.

With the corrected dependency set, the real-cluster workflow passed in 60.65
seconds; the acceptance package took 61.696 seconds. The compiler pin is
`e8374ca995c6ea3964b316250645f29bc57d7d32`. Pinned regeneration reported no
changes or drift. Controller tests also reject unsupported provisioning settings
before Kubernetes access and retain cleanup for records with invalid settings.

The last real-cluster run, including unsupported-setting checks, passed in
60.18 seconds; the acceptance package took 61.217 seconds. Compiler CI
[34313105365](https://github.com/jsell-rh/stego/actions/runs/34313105365)
passed on the pinned revision.

The final full race suite passed with PostgreSQL and Keycloak required. Its
acceptance package took 325.383 seconds. The separate Kubernetes run and the
focused controller tests cover the final provisioning checks. Module verification
and formatting checks passed.

The database controller now uses STEGO's generated Kubernetes client. The
compiler pin is `e6b4d6ceb198c89c9ad4eaedb486a1b2e81e7037`. Common HTTPS requests,
ownership checks, update preconditions, and deletion checks are generated.
Hypershell retains its database definitions, placement, and readiness rules.
The client retains exact JSON numbers; readiness checks require valid integers.

The real-cluster workflow with this client passed in 69.77 seconds. Deletion
replay passed in 2.93 seconds; the acceptance package took 73.740 seconds. Five
stable reconciliations took 75.8 ms and did not change the Deployment. These
results check the client extraction; they do not establish production capacity.
The compiler and variant vulnerability scans reported no known vulnerabilities.

The generated-scanner replay change passed the complete database workflow on
2026-09-09. The real workload took 65.21 seconds. Replay with C and ICU ordering,
API restart, denied capability confirmation, and empty results took 8.56 seconds.
The acceptance package took 74.819 seconds. Static checks and pinned regeneration
also passed. The compiler remains at `639b95bb49bc9020b849f5f9ee6180a7b1a1ee09`;
this change reuses its scanner without a new runtime API.
