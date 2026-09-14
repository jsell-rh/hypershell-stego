# Database controller write permissions

Conditional database patches require STEGO's generated exact grant policy.
The operation is `observe.provider`, the resource is `ManagedDatabase`, and the
target is the database's stored managed-cluster ID. The supported providers are
`cnpg` and `external`. Use a canonical cluster ID in the grant. For example:

```sh
HYPERSHELL_CONTROLLER_WRITE_GRANTS='[{"issuer":"https://issuer.example","subject":"database-controller","resource":"ManagedDatabase","operation":"observe.provider","target":"0ujsswThIGTUYm2K8FjOOfXtY1K"}]'
```

The caller also needs a verified token, a subject in
`HYPERSHELL_CONTROL_PLANE_SUBJECTS`, and the current resource revision. Missing
grants deny the write. Cleanup grants and Gateway grants do not authorize
database observations. An administrator role does not replace these checks.
Grant changes require restart of every API instance.

The controller can update `status`, `connection_secret`, or both. Empty patches
and patches with only desired settings are denied. A patch that mixes observation
fields with other fields is invalid. The API checks stored placement and the
field set inside the mutation transaction, before it applies changes. The request cannot change its provider
or cluster to select another grant. The catalog contract makes both fields
immutable after registration.

The cluster ID limits this grant to that cluster's database records. It covers
both supported providers in that cluster. It does not limit access to one
database ID or prevent two provider processes from acting at the same time.
The grant also does not prove the physical location of an external server.

These API grants control stored observations. They do not revoke provider
credentials or undo provider work that occurred before a denied status write.

`TestDatabaseControllerWriteGrantsAcrossClustersAndRestart` registers CNPG
servers in two clusters through REST. It checks wrong subjects, wrong clusters,
wrong operations, cleanup-only access, missing grants, unconfigured subjects
with explicit grants,
administrator roles, and invalid field groups. Denied writes must leave the
whole row and committed database event count unchanged. A test trigger records
events after insertion, so outbox delivery cannot hide an extra committed event.

Successful observations must advance one revision, store the requested fields,
commit one event, and deliver it through the generated runtime. A REST request
cannot bypass the revision requirement. The test removes a grant, restarts the
API, and requires denial with the same token. The controller for the other
cluster can still publish its authorized observation.

## Current contract evidence

On 2026-09-14, the bounded jshell Job `stego-placement-005aace4/check` passed
all five updated database contract tests with the race detector. The acceptance
package took 60.167 seconds. The tests checked:

- Cluster-specific grants, denied writes with no stored changes or extra events,
  and grant removal after API restart.
- Stale and malformed revision rejection, transaction rollback, and REST and
  gRPC behavior after restart.
- Atomic cleanup observations, retained reads, and recovery after a replay
  stream stopped responding.

These tests create CNPG records through REST with explicit `cluster_id` values.
They do not use the old SQL trigger to infer placement. The catalog fixture
seeds a cluster without a database, so each test registers its own server.

All 230 generated files and build records matched across two generation runs,
the tests, and the checkout. The Job completed, and its namespace was removed.
Local evidence is in `/tmp/hypershell-database-contracts-2ei31ep9`.

This test group checks the control-plane contracts. It does not run PostgreSQL
provisioning through CNPG. The separate live Gateway result and remaining work
are recorded in [Database providers and locality](database-providers.md).
Older workload fixtures and the console asset archive still prevent a full CI
pass.

## Earlier evidence

The following results used the former deployment provider and provider-name
grants. They do not validate the current placement contract.

The baseline failed in 3.21 seconds: an identity controller without a database
grant could publish database status. The focused permission check passed in
5.48 seconds after the fix. The existing database revision test also passed;
it checks stale observations, malformed preconditions, event rollback, and
restart. The catalog workflow now requires denial when a controller tries to
change the desired engine version, then proves that an administrator can make
that change. The Gateway grant regression also passed.

The real database Kubernetes workflow passed on 2026-09-10 with the explicit
provider grant. Provisioning, TLS access, persistence, foreign namespace denial,
offline deletion, and late-effect cleanup took 64.33 seconds. Replay under C and
ICU ordering took 12.18 seconds. The acceptance package took 77.547 seconds.
These tests verify the permission change; they do not establish production
capacity.

The complete Gateway Kubernetes gate passed in 213.285 seconds. Deletion before
workload startup took 51.27 seconds. The live database and identity workflow took
160.97 seconds. It verified owner and viewer access, denied writes, provider
persistence, Pod and database restart, namespace replacement, offline deletion,
and cleanup of late resources on the former cluster.

The complete PostgreSQL/Keycloak race suite passed with a 664.210-second
acceptance package run. It includes the new permission test, stale-observation
rejection, transaction rollback, event delivery, and restart. Module verification,
formatting, and `go vet` also passed.

STEGO owns grant parsing and exact matching. Hypershell supplies the field set,
operation name, cluster target, and transaction call. No compiler change is
required for this policy.

The current provider requirements and remaining work are in
[Database providers and locality](database-providers.md). A passing observation
grant test does not prove SQL permissions, credential ownership, or protection
against concurrent provider processes.
