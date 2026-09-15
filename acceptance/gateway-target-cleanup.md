This page records an earlier database-catalog release and its test results.
Its `ManagedDatabase`, database placement, and migration instructions do not
apply to the current release. Use the
[current database contract](controller-local-database.md). The current API has
no database selection field. Installation supplies the PostgreSQL server, and
the assigned controller manages each Gateway's logical database and login.

Gateway workload cleanup in that release used STEGO's target history. The service declared:

```yaml
cleanup_owners: [identity, workload]
cleanup_targets: {workload: cluster_id}
```

The generated storage trigger records the cluster before provider work. A cluster
change adds a target and preserves the old target. The private state API returns
both targets, their observations, and the resource revision from one retained
read. The public Gateway messages do not expose this controller metadata.

Each workload controller uses its configured cluster ID. After Gateway deletion,
it checks its recorded target even when the Gateway now names another cluster.
It does not need a remaining local object to find the obligation. Provider
operations still require matching ownership labels, object identity, and revision
preconditions. A cluster with no recorded target performs no cleanup.

The controller records success only after its Gateway and sandbox resources are
absent. It uses the observed Gateway revision, and the observation commits with
its deletion event. A conflict requires fresh provider work. Each target preserves
the other targets and the identity owner's observation. The workload owner is
complete only when all recorded cluster targets are complete.

Periodic checks continue after success. A later pending or failed provider check
requests a false observation for that target. An unchanged observation does not
produce another event. If an observation write fails, a later pass must retry.

The REST and TLS gRPC acceptance check rejects a new move that would separate
the Gateway from its database. With the API stopped, it restores a fixture from
an old installation: a moved Gateway with both cluster targets and an unassigned
CNPG server. It restores the current constraints before API startup. This tests
old data without permitting new remote placement.

The check deletes the Gateway, records each cluster separately, restarts the
API, and checks independent identity cleanup. It also checks stale revisions,
denied callers, unrecorded targets, event rollback, delivery, and reopened work.
Both REST and gRPC must deny cluster deletion while its cleanup is pending.
Denied deletion must change neither the cluster record nor its events. The
current cluster remains blocked while any workload target is unfinished.
After both targets complete, both cluster deletions must deliver their events.

STEGO supplies the reference and target-history query. Hypershell selects the
Gateway's workload owner and cluster reference in its catalog deletion rule.

The earlier Kubernetes workflow moves
the Gateway before deletion. It then removes the former cluster's resources,
restarts its controller, creates a late namespace with a blocking finalizer,
and verifies that cleanup becomes pending again. After another restart and
finalizer removal, the old target completes while the new target stays pending.

The workload owner covers Gateway and sandbox resources managed by that
controller. The associated ManagedDatabase has its own cleanup obligation.
A workload confirmation does not prove database removal or permit parent purge.
The database provider still has no declared history of cluster placement.
Cluster IDs must remain bound to their provider locations. Rebinding the same
cluster ID to a different Kubernetes cluster is not covered by this history.
Live moves do not yet remove former targets before Gateway deletion. Cleanup
observations now require exact grants. Other controller operations still need
separate permissions. Cross-process fencing, target retirement, and parent
finalization remain open.

Apply the generated migrations and update all API instances before controllers.
The compiler revision is in [the pin file](../.stego/compiler-revision).
Enabling target history on existing rows requires an explicit history migration.
The generated migration refuses to infer earlier locations from the current
cluster field. No history import protocol is provided yet. New databases and
restarts of the same target contract are covered by the application tests.

Historical validation on 2026-09-09 passed the controller race tests, the focused API
checks, the complete Gateway Kubernetes workflow, and the full application race
suite. The Gateway workflow took 243.581 seconds; the full acceptance package
took 555.058 seconds. Pinned regeneration reproduced all 69 generated, dependency,
and state file hashes. These results predate the local database requirement.
Run current application checks in CI or a bounded jshell Job.

Cleanup observation writes also require [explicit grants](cleanup-permissions.md)
for the verified issuer, subject, resource, operation, and target.

## Locality and former-cluster check

On 2026-09-14, the bounded jshell Job `stego-placement-55ba7679/check` compared
the old generated query with compiler `97d3877237bcd507a1bdba876016726b0fab6d09`.
The old query returned HTTP 204 for a former cluster with unfinished cleanup.
The fixed query passed the REST and TLS gRPC checks. The target-history test
took 11.54 seconds. All seven selected application checks passed under race
detection in 43.250 seconds.

The checks cover new-move denial, retained former-cluster cleanup, independent
owner and target observations, grant revocation, restart, reopened work, event
rollback, and event delivery after successful parent deletion. They also cover
local database registration, legacy row preservation, controller access, and
parent deletion concurrent with Gateway creation.

An earlier attempt stopped before this comparison because its event audit also
counted identity events. The final audit selects Gateway events and cluster
deletion events. No production access or event rule was weakened.

Results are in `/tmp/hypershell-former-parent-xemnouyd`. Both generation passes
and the post-test files have identical hashes for all 230 generated and build
files. The collected output supplies the committed generated files. The Job
completed, and its namespace and private launch files were removed. The full
compiler CI run also passed for this compiler revision.

This check uses PostgreSQL and the generated API runtime. It does not deploy
Gateway Pods or database servers. The separate CNPG browser workflow supplies
that evidence; external PostgreSQL provisioning remains open.
