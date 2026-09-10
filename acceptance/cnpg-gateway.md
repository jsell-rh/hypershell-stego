The complete fresh-cluster CNPG Gateway gate passed on 2026-09-10 with race
detection. The workflow took 334.73 seconds; the acceptance package took
335.777 seconds. It used compiler `b3886715adfb6cdad57c099bd33351fe65ca83d5`.
The deployment-provider regression also passed. This is application evidence
for the shared provider, not a production capacity or HA claim.

Run `scripts/check-workload.sh cnpg-gateway` with the same isolated PostgreSQL
setup used by the other workload tests. The script installs pinned Kubernetes,
cert-manager, CNPG, and agent-sandbox releases. It starts the real Gateway image,
identity provider, generated API, event runtime, and controllers. No Playwright is used.

The Gateway controller creates separate SQL databases, login roles, passwords,
and source encryption keys in the shared database namespace. SQL names encode
all bytes of the canonical Gateway ID. The role and database names match the
shared Cluster's `sameuser` connection rule. Gateway connections use
`sslmode=verify-full` with the CA from the current CNPG Cluster. CNPG-generated
Secrets must refer to that Cluster's UID.

Roles use the Cluster's inline managed-role list. CNPG can repair direct SQL
role changes through that path when its Cluster reconciles. The live gate found
that this did not occur within 150 seconds in an otherwise stable Cluster.
The controller now checks SQL authentication, role flags, role memberships, and
database settings on each pass. A confirmed role mismatch requests operator
repair with `cnpg.io/reloadedAt`. Network failures do not cause reload requests.
Password Secrets carry the documented `cnpg.io/reload` label. Each role has no superuser, database-creation,
role-creation, replication, or RLS-bypass permission. It has no role memberships
and permits at most 32 connections. This limit is a conservative initial value;
it is not a measured production capacity.

STEGO supplies `PatchOwned` for changes computed from a prior resource read.
The patch uses that exact UID and version. A conflict requires a fresh read and
a new role list. The application preserves other roles and declares only its
own role policy. It does not add another controller loop or retry mechanism.
The generated controller runtime still owns scheduling, retries, event recovery,
deadlines, and conditional cleanup observations.

The Database resource must report the current generation as applied. Role
readiness also checks the configured password Secret version. These observations
are not a cross-system transaction or proof against a concurrent external SQL
change. The explicit SQL check supplies current evidence. Stable reconciliation must
not keep changing the Cluster generation or reload annotation. A mismatch in
SQL database ownership or settings fails closed; that condition still requires
operator repair of the Database resource or an administrator restore.

Deletion first removes the Gateway and sandbox workloads. It then deletes the
owned Database resource and waits for absence. The role declaration changes to
`ensure: absent`. An old role status cannot finish cleanup. A bound SQL query
must confirm that both the role and database are absent before the controller
removes retained passwords and encryption keys. SQL access failure leaves
cleanup pending. One Gateway deletion preserves the shared Cluster and the
other Gateway's data and keys.

Initial key and credential creation also requires SQL absence evidence. This
prevents new keys from being applied to retained SQL data after loss of the
Kubernetes identity records. The gate supplies SQL state with no Gateway
Kubernetes records and checks that no keys or credentials are created. It then
removes that test SQL state and checks normal provisioning.

Absence checks use the bootstrap application credentials. Readiness checks
use the Gateway's own SQL role and stored password. Each check uses verified TLS,
one connection, a six-second operation limit, and a five-second statement limit.
The session defaults to read-only. The query is fixed and its identity argument
is bound. It does not need superuser credentials. The controller normally uses
the database service DNS name. `HYPERSHELL_CNPG_DIAL_ADDRESS` can supply an IP
address and port for an explicit route, such as the private NodePort route in the acceptance test.
The TLS server name remains the derived service DNS name. The override preserves
certificate checks and supports one database route. `PGSERVICE` must be unset.
The client sets explicit parse values and session parameters, so unrelated
PostgreSQL environment settings cannot select files, credentials, plaintext
fallbacks, or another server.

Absent role declarations remain in the Cluster to repair late role recreation.
The list has a limit of 1,024 entries, including these declarations. Exceeding
that limit fails closed. An archive policy, larger capacity, and measured
throughput remain open. This single-instance profile is not a production HA or
backup contract.

The deployment Gateway regression passed both workflows under race detection
in 215.319 seconds with strict field validation. Cleanup, provider-deadline,
and backlog regressions passed in 78.002 seconds. Contract tests, CLI build-record
checks, provider race tests, and `go vet` passed. The complete CNPG result is
recorded above. Pinned regeneration preserved all 83 generated and dependency
file hashes.

The workflow exposed two provider defects. Inline role status did not trigger
bounded SQL repair; explicit SQL checks now request repair when needed. The
Database resource also used `reclaimPolicy` instead of `databaseReclaimPolicy`.
Kubernetes pruned the field, so the SQL database remained after resource deletion.
The SQL absence check prevented false completion. The corrected field declares
`delete`. STEGO's `kubernetes-client` 1.3.0 now requires strict field validation
for create, replace, and patch requests. A supporting API rejects unknown fields
instead of silently discarding them.

The test and client configuration required these corrections:

| Finding | Correction |
| --- | --- |
| CNPG retained its default 1,800-second Pod termination period. | The restart fault uses a 30-second grace period and still requires recovery of data and original keys. |
| pgx treated an empty service option as a service lookup and read a CA file before evaluating the initial SSL mode. | Omit the service option, reject `PGSERVICE`, and set explicit file options before supplying verified TLS in memory. An environment test covers the configuration. |
| `kubectl port-forward` closed its listener after a PostgreSQL connection reset. | Use an isolated private NodePort route and retain the original TLS server identity. |
| A restart check stopped a new process before it installed its signal handler. | Wait for the new controller's first completed scan. Abnormal shutdown still fails the test. |
| CNPG repaired a password before another reload request was needed. | Test authentication with the original Secret. Keep the existing SQL Pod retry and time limits. |

The run before these final test corrections completed every CNPG application
check but failed process shutdown in 335.611 seconds. The next run failed its
redundant reload-marker assertion in 380.049 seconds, while a direct check
confirmed that the original password already authenticated. Neither failed
package result is reported as a passing gate.
