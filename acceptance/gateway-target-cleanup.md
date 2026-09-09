Gateway workload cleanup now uses STEGO's target history. The service declares:

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

The REST and TLS gRPC acceptance check moves a Gateway, deletes it, records each
cluster separately, restarts the API, and checks independent identity cleanup.
It also checks stale revisions, denied callers, unrecorded targets, event
rollback, delivery, and reopening one target. The real Kubernetes workflow moves
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

Local validation on 2026-09-09 passed the controller race tests, the focused API
checks, the complete Gateway Kubernetes workflow, and the full application race
suite. The Gateway workflow took 243.581 seconds; the full acceptance package
took 555.058 seconds. Pinned regeneration reproduced all 69 generated, dependency,
and state file hashes. Use `scripts/check-gateway.sh` and
`scripts/check-gateway-workload.sh` to repeat the application checks.

Cleanup observation writes also require [explicit grants](cleanup-permissions.md)
for the verified issuer, subject, resource, operation, and target.
