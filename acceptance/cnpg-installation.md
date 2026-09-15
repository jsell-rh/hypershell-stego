The CNPG test installation must supply PostgreSQL before any Gateway controller
starts. It must use a dedicated namespace that is not a Gateway allocation.
The API has no database catalog, database ID, or server registration call.
Gateway controllers own logical databases and logins through STEGO's SQL runtime.
They must not create CNPG Cluster, Database, or DatabaseRole resources.

`scripts/cnpg-test-operator.py prepare` now requires an explicit
`--database-namespace stego-cnpg-database-<suffix>`. It no longer accepts a
catalog ID or derives a namespace from one. Its operator watch and webhooks
select that namespace. The operator starts with zero replicas. Its existing
40-minute lifetime Job, resource limits, and ownership journal stay in place.
The caller must create and remove the installation database namespace while
the operator can process finalizers. The helper refuses existing resources,
a repeated plan, or operator removal before the database namespace is absent.

The old `check-cnpg-database.py` runner is removed. It required deleted catalog
tests, registered a database through the retired API, and checked the old
allocator profile. Historical evidence retains its source hashes. Its source
is available at [the last runner revision](https://github.com/jsell-rh/hypershell-stego/blob/07750dc7160f49099ea7c898a622b87737f4c2a4/scripts/check-cnpg-database.py).

The next installation fixture must run the complete Gateway browser workflow
against a real CNPG server. It must keep the API and session database separate,
use verified TLS, give workers a limited SQL account, and preserve installation
data after Gateway deletion. Namespace and Pod selectors must keep the worker's
network access valid when CNPG replaces a database Pod. The workflow must also
verify a real CNPG restart or failover, retained SQL identities and keys, and
complete resource cleanup. Server creation belongs to the test installation.

Four small local tests passed for namespace validation, watch and webhook scope,
resource and time limits, existing-resource refusal, and cleanup ordering.
The helper change does not prove CNPG application behavior. The required CNPG
CI gate remains unsuccessful until the full installation workflow passes.

The complete browser test now has a CNPG fixture input:
`STEGO_TEST_GATEWAY_SQL_FIXTURE_FILE`. The operator supplies this private file
with the installation namespace and Cluster UIDs, the fixture administrator
password, and its CA. The test checks file type, size, and access mode. It
requires the dedicated namespace label and the same live Cluster UID before
it connects. The file is not a Gateway API input or a controller file.
The normal fixture still uses verified loopback PostgreSQL.

The CNPG path uses the installation's `gateway-database-rw` service. The test
creates the same limited provisioning account and runs the same Gateway SQL
isolation, fault, encryption, and deletion checks. The API and console keep
their separate fixture database. The worker receives no CNPG administrator
password or CNPG object permissions.

For this fixture, `prepare-browser-inspection.py` accepts
`--cnpg-database-namespace stego-cnpg-database-<suffix>`. STEGO generates one
additional worker egress rule for that namespace, the `cnpg.io/cluster` label,
and TCP port 5432. The render check rejects changes to other worker resources
or permissions. Eight local inspection tests and frozen generation passed.

The CNPG restart check deletes one observed primary Pod with a UID precondition.
It retains the Cluster and requires a different Pod UID, two ready instances,
an unchanged Cluster specification, and preserved SQL object IDs, credentials,
keys, provider data, and installation data. The evidence records whether the
primary name changed; a Pod replacement is not assumed to be a failover.
The normal sidecar restart now checks SQL object IDs as well.

These source paths have not yet passed a live CNPG run. The remaining installer
must create the bounded server, supply the private fixture file and namespace
permissions, run the complete browser workflow, and verify final cleanup.

The installation runner is `scripts/check-cnpg-installation.py`. Prepare a new
frozen source directory with the CNPG namespace option, then run that frozen
runner with these explicit inputs:

- `--operator-context`: the saved operator context.
- `--ci-kubeconfig`: the private, short-lived CI kubeconfig.
- `--source`: the frozen source directory.
- `--repository`: the local repository used to verify the recorded commit.
- `--results`: a new evidence directory.
- `--storage-class`: dynamic storage with the `Delete` reclaim policy.

The runner verifies the full source inventory against the commit before it
writes to the cluster. It requires at least 45 minutes on the explicit CI
credential before installation. The inner browser runner checks its 35-minute
budget again after the server is ready. This avoids installing the operator
and database with a credential that is already too short for the test. See
[the credential contract](ci-credentials.md).

The runner acquires the shared live-test Lease, installs the
scoped operator and server, and supplies a private Secret to only the test
container. The browser runner uses the CI identity and verifies the same Lease
UID and test target. It cannot release the Lease while the server remains.
The operator stays limited to 40 minutes; the server has a 25-minute lifetime.
The extra operator time permits finalizer cleanup after server termination.

The server has two instances with fixed CPU, memory, storage, and connection
limits. Its network rules allow only the required SQL, replication, operator,
DNS, and Kubernetes API traffic. It uses a pinned PostgreSQL 18.6 image.
The test account can read the named CNPG Cluster and SQL service and replace
Pods in the dedicated database namespace. It cannot read server Secrets or
write CNPG Cluster resources. Production workers receive none of these rights.

The runner records a create intent before each server resource write. If a
create response is lost, it records a matching observed owner for cleanup and
stops that attempt. Cleanup checks resource UIDs and the run owner, waits for
confirmed absence, and retains the Lease if any application, allocation, or
server resource remains. Missing or failed application and restart evidence
cannot produce a passing result.

The local boundary, projection, lock, evidence, and inspection checks pass.
No CNPG server has been installed or tested yet for this implementation. The
GitHub CNPG gate remains unsuccessful until its installation and CI execution
path has passed. A manual operator-assisted pass will be recorded separately.

The first live installation attempt used source `90b6ca5`. Both CNPG instances
became ready. The application Pod then failed to obtain CPU and memory within
its 180-second startup limit. No application test ran. Cleanup removed the
application Job, database namespace, private fixture, operator resources, and
both recorded persistent volumes. The shared Lease was released. See the
[failed attempt record](cnpg-installation-attempts.json).

OpenShift added a worker node. After that node became ready and cleanup was
confirmed, the same frozen source started a second bounded attempt. No test
limit or application permission changed.

The second attempt created both Gateways and passed verified TLS, SQL isolation,
and unsafe-grant recovery. Primary replacement then exposed an installation
permission error: the test removed public `CONNECT` access from `postgres`
without an explicit grant for CNPG's `streaming_replica` role. The promoted
primary was ready, but the former primary could not connect for recovery
(SQLSTATE `42501`). This is a failed recovery attempt, not a passing workflow.

The corrected installation fixture grants only `CONNECT` on `postgres` to
`streaming_replica`. It retains the public access revocation. Gateway accounts
receive no maintenance grant. CNPG uses this connection for primary recovery
and `pg_rewind`; see the [CNPG replication contract](https://cloudnative-pg.io/docs/devel/replication/).
An installation that restricts public database access must preserve its server
maintenance roles explicitly. This belongs to installation setup; Gateway
controllers must not change unrelated database permissions. The complete
restart and cross-database denial tests remain required for this correction.

The corrected source `327f24c` passed the complete CNPG browser application
workflow in 462.01 seconds. CNPG promoted its second instance, restored the
former primary as a replica, and retained the required data. Namespace recovery,
credential encryption, SQL cleanup denial, both Gateway deletions, and browser
session checks also passed. The final cleanup list request failed, so the outer
run returned a failure. This is an application pass with a cleanup observation
failure; it is not an unattended CI pass.

Cleanup reads now have three bounded attempts. Invalid replies and persistent
errors still fail without a success record. The outer runner also checks all
labeled test data before it can release the Lease. Four small tests cover a
single read failure, repeated timeouts, invalid replies, and remaining objects.
The original failed run record is retained. A separate read-only check must
confirm absence and verify the application evidence.

Independent verification checked all 881 source files, all 229 generated files,
three matching generation records, 16 CI access checks, 57 application access
checks, and six admission probes. CNPG recovery took 69.44 seconds and namespace
recovery took 44.15 seconds. Read-only checks with the CI and operator identities
confirmed absence of application data, allocations, the database namespace, all
26 operator resources, and both recorded volumes. The shared Lease is free.
See the [verified application and cleanup record](cnpg-installation-evidence.json).
The original nonzero runner result is retained; unattended CI remains open.

The next run uses frozen source `eb53ea7` and compiler `5e9c89d`. Preparation
verified all 891 source files against the commit, generation and drift, and the
five allowed fixture changes. The source is
`/tmp/hypershell-cnpg-installation-source-v5`; its dedicated database namespace
is `stego-cnpg-database-20260915-v5`. See the
[preflight evidence](cnpg-installation-preflight-evidence.json).

This source includes the verified viewer and account checks, the cleanup read
retry, and the new credential check before installation. Eight credential
checks, six CNPG workflow checks, six server fixture checks, and four operator
checks passed. The frozen runner import also leaves the source inventory
unchanged. No server has been installed for this run. Wait for the active
browser and queued API runs to finish, verify cleanup, and renew the CI
credential before acquiring the shared Lease.
