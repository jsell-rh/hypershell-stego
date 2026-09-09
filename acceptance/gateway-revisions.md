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

Gateway also declares desired inputs and a `workload` observation group in
`service.yaml`. STEGO generates the generation counter, group metadata,
conditional group writer, and current-state projection. The domain declaration
supplies `Provisioning` and `ObservationPending` as unobserved display values.
The variant supplies access policy and provider work.

Phase and status are controller-owned. Owner creation and patch requests that
set them fail with HTTP 403 or gRPC `PermissionDenied`. Controller observations
must contain both fields and cannot also change desired inputs. The API writes
the group and its event in the same transaction. A desired change advances the
generation. A fresh observation can confirm the new generation even when its
phase and status values have not changed.

REST and gRPC reads use the generated current-state projection. Generated list
queries use the same values for search, access filters, ordering, and counts.
Stored older observations remain available for diagnosis. Service-account
creation also requires a current Gateway workload observation. A stale Healthy
value cannot satisfy this check.

The regression also checks owner write denial, mixed-field rejection, stale
status presentation through REST and gRPC, filtered lists, restart, and fresh
confirmation. `TestServiceAccountRequiresCurrentGatewayObservation` checks that
a desired change blocks credential creation before it reaches the provider.
The fixture then supplies a fresh observation and creation succeeds.

Existing deployments must apply
`out/storage/migrations/000003_resource_generations.sql` before this application
starts. Use a migration role. The application role must not own the schema or
have DDL, TRUNCATE, or trigger-bypass privileges. Startup fails if the generated
contract is absent or differs. A changed generation contract invalidates older
observations and revision tokens. It preserves retained deletion rows. Schema
changes and invalidation must share one transaction. See STEGO's
`specs/resource-generations.md` for the migration contract and table-lock cost.
Deploy the API and controller changes together; old controllers cannot publish
status through the new contract.

The generation covers declared fields on this Gateway. Referenced-resource
changes, provider changes, and external drift still require periodic observation.
The workload controller does not skip provider checks when its observation is
current. These checks cannot revoke an external action already in progress.
Database status revisions, per-subject group authority, durable cleanup
completion, cross-process fencing, condition history, and production capacity
remain open. The automatic STEGO CRUD facade does not yet support observation
ownership; this variant uses its explicit reference API facades.

## Validation

The pinned remote compiler is
`5f93831820a363104249123b92776e125ba7fe40`. Its local full test suite and GitHub
checks passed with race detection. The focused application checks passed with
PostgreSQL and real Keycloak in 87.603 seconds. The separate stale-readiness
regression passed in 1.363 seconds.

The real Kubernetes Gateway workflow passed in 232.419 seconds. Deletion before
controller startup took 55.35 seconds. The complete workload took 176.02 seconds
and covered database and OIDC setup, owner and viewer access, denied writes,
service accounts, Pod and database restart, namespace replacement, stable keys,
and offline deletion. The script removed its temporary cluster. These local
checks do not establish production capacity or complete reconciliation coverage.

The full package run took 505.351 seconds. It found four fixture failures caused
by the new ownership and observation rules. The fixtures now supply fresh
controller observations and consume the resulting event before testing deletion.
All four affected tests then passed with race detection and real Keycloak in
109.763 seconds. All other tests passed in the full run. Module verification and
`go vet ./...` also passed.

A local filtered-list measurement used 200 Gateways, 100 visible to the caller,
and pages of 20. Each of three paired samples read 100 pages. Before this change,
samples averaged 9.17–9.72 ms per page and allocated about 96.8 kB. With generation
metadata and current observations, samples averaged 9.60–10.97 ms and allocated
111–113 kB. The new contract has a measured allocation cost and higher latency
in two samples. This small test does not establish concurrent capacity or a
production latency bound. Query and allocation profiling remain required work.
