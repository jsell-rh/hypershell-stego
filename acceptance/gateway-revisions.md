# Gateway observation revisions

Gateway enables the STEGO `versioned` entity contract. PostgreSQL supplies its
revision. Workload and identity controllers read the revision from the privileged
state API before external work. They send it through the generated gRPC metadata
helper when they publish an observation. The public protobuf message shapes stay
unchanged.

The API checks the configured control-plane subject. It then checks the revision,
updates the row, and inserts its event in one transaction. A stale revision returns
`Aborted`; the controller must read current state and repeat external work. A
missing revision returns `FailedPrecondition`. Malformed metadata returns
`InvalidArgument`. Supplying a revision does not give an owner controller access.
Controller writes through the ordinary REST patch path return HTTP 428.

`TestGatewayRejectsOldObservationAcrossRESTGRPCAndRestart` is the permanent
regression for the stale-status failure found during the PR 200 review. It:

1. Creates a Gateway through REST and consumes its event.
2. Reads the controller state, then changes desired DNS through REST.
3. Rejects status from the older observation through gRPC.
4. Rejects missing, malformed, duplicate, and unauthorized preconditions.
5. Checks that rejected writes change no state and enqueue no event.
6. Forces event insertion to fail and checks revision and status rollback.
7. Restarts the application and checks that the old revision still fails.
8. Accepts a fresh observation, delivers its event, and reads its status through REST.

The focused regression and existing mutation and controller-subject tests passed
with race detection on 2026-09-09. The three tests took 11.926 seconds in total.
The common PostgreSQL revision and gRPC metadata contracts have separate tests
in STEGO. The variant contains only Gateway policy, mappings, and provider work.

Existing deployments must apply `out/storage/migrations/000002_resource_versions.sql`
before starting this application. Use a migration role. The application role must
not own the schema or have DDL, TRUNCATE, or trigger-bypass privileges. Startup
fails if the generated revision schema is missing or differs. The migration
preserves existing revisions and does not remove retained deletion rows.
Deploy the API and controller changes together; old controllers have no revision
precondition and cannot publish status through the new API.

This change rejects stale Gateway status commits. It does not revoke an external
action already in flight. It does not yet provide desired generations, observed
generations, field ownership, database status revisions, durable finalizers, or
cross-process fencing. Ordinary owners still have the reference API's mutable
phase and status fields. Existing status can also describe an earlier desired
state until the next observation; generation-aware presentation remains required.
Keep periodic provider observation and drift repair enabled.

The real Kubernetes Gateway workflow also passed with the revision contract.
The two acceptance tests took 226.078 seconds: deletion before controller startup
passed in 56.71 seconds, and the full Gateway workload passed in 168.32 seconds.
The latter covered database and OIDC setup, owner and viewer access, denied
writes, service accounts, Pod and database restart, namespace replacement,
stable keys, and offline deletion. The test removed its temporary cluster.

The pinned remote compiler `edc7ca0efa941b629ee78e21d3f3a549675981c3`
reproduced all 63 generated, state, and dependency files byte for byte in a separate
copy. Module verification and `go vet ./...` also passed. These checks do not
establish production capacity or complete reconciliation compliance.

All application acceptance tests passed with PostgreSQL, real Keycloak, and race
detection required. The acceptance package took 522.757 seconds. The wider package
run found a sandbox-count test that assumed an exact write sequence. An initial
recovery scan can repeat the current count. That test was changed to allow this
repeat while still rejecting stale values and requiring progress. Production
scheduling code did not change.

A local gRPC patch measurement ran three samples of 100 requests through a
separate generated process. With revisions, samples averaged 4.41–4.92 ms per
request. The prior commit, tested on the same machine, averaged 4.31–5.11 ms.
Each request includes authentication, an owner check, a serializable write, and
an event insert. Startup and connection setup are excluded. The ranges overlap;
this small sample does not establish zero overhead, concurrent capacity, or a
production latency bound.
The corrected sandbox-count package then passed 50 runs with race detection in
125.548 seconds. All other packages passed in the full run or had no tests.
