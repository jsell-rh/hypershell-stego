# Observation deadlines

STEGO controller 1.7.0 generates `RunObservation`. The Gateway workload, database,
and Gateway identity controllers use it to reserve two seconds for an observation
write within each existing 20-second action. Provider work receives an earlier
deadline. The write receives a separate context with the same parent.

The failure tests first record a healthy resource or a complete cleanup. The next
provider call waits until its context expires. Before this change, the controller
used the expired context for the observation write. The API could thus keep
`Healthy`, `ready`, or a previous cleanup confirmation after provider failure.

The five deadline workflows check these transitions:

| Resource and operation | First observation | After provider timeout | After recovery |
| --- | --- | --- | --- |
| Gateway workload | Running / Healthy | Degraded / WorkloadUnavailable | Running / Healthy |
| Database deployment | ready | error | ready |
| Gateway workload cleanup | complete | pending | complete |
| Database cleanup | complete | pending | complete |
| Gateway identity cleanup | complete | pending | complete |

Each workflow creates the resource through REST. Cleanup tests also delete it
through REST. Controllers use TLS gRPC and the generated runtime. The tests check
stored observations and generated event delivery, restart the API, check the
stored failure again, and then restart the controller with a working provider.
Deleted resources remain unavailable through the public API. Gateway recovery
must confirm the current generation. Database recovery must advance its revision.

The controller still uses the revision read before provider work. Exact grants,
conditional updates, and atomic observation events remain required. A successful
failure write does not erase the provider error. The runtime must retry through
a fresh state read. Identity configuration uses the same time reserve, but a
provider failure does not create a new identity status field.

The cleanup test also exposed a database watch defect. gRPC can return no headers
and report an initial failure through `Recv`. The capability check now preserves
that error. A temporary conflict can reconnect; authentication and permission
failures remain terminal. A clean stream end without the required capability is
still rejected. An in-memory gRPC server tests all four error codes and a clean end.

The time reserve cannot guarantee a successful write. API outages, permission
changes, concurrent revisions, or parent cancellation can prevent it. A write
that times out might already have committed. The next action must read current
state. The helper does not detach callbacks or write after parent cancellation.
Provider callbacks must honor their context.

Durable retries, observation timestamps, freshness after controller loss, and
cross-process fencing remain open. These tests establish bounded deadline
behavior in the local fixture. They do not establish a production recovery target.

## Validation

On 2026-09-10, the five deadline checks passed together in 116.907 seconds. The
complete PostgreSQL and Keycloak race suite passed with 112 acceptance tests and
a 917.518-second acceptance package run. The real Kubernetes database gate passed
in 81.938 seconds, and the complete Gateway gate passed in 231.422 seconds. The
controller unit suites, `go vet`, and module verification also passed.

The checks used Go 1.26.8 on Linux amd64, PostgreSQL 18.6, and the repository's
pinned provider fixtures. The application pins compiler revision
`46b5f4e5327dfd056cafb65fe3a39ff0cde74500`. The Kubernetes gates ran alongside part
of the full suite. These elapsed times describe this local run, not production
latency or capacity.
