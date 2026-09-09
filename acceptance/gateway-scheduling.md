The Gateway workload controller uses STEGO's generated `RunKeyedWatch` runtime.
Hypershell supplies its watch, retained-state scan, and provider actions. It does
not keep another scheduler, retry map, or worker pool.

The controller permits four concurrent actions for different Gateway IDs. Each
ID has at most one active action within a `Run` call. Repeated events share one
key. An event received during an action schedules another current-state pass.
Failures retry with delays from one to ten seconds. Events cannot bypass that
delay. Periodic scans continue after successful provider observations.

The watch opens before the scan. A failed watch cancels and joins the session
before reconnect. Reconnect opens a new watch and starts a new retained-state
scan. Source and action calls must honor cancellation. Provider methods must
support concurrent calls for different IDs. Storage revisions and provider
identity checks remain necessary for every write.

`TestGatewayCleanupMakesIndependentProgressAfterRestart` creates and deletes two
Gateways through REST, drains their events, and restarts the API. The controller
then discovers both retained records. A controlled test provider holds one
deletion call. The other Gateway must record completion through TLS gRPC and deliver its event
through the generated runtime. The blocked Gateway must remain pending. After
its provider call is released, it must also complete and deliver its event.
Public reads continue to return 404. The separate Kubernetes gate runs the
actual Gateway provider workflow; it does not measure a large concurrent fleet.

The test failed against the FIFO controller because the blocked call prevented
the second cleanup. It passed after the controller used the generated keyed
watch runtime. STEGO's generated tests separately check 10000 duplicate events,
changes during active work, per-key exclusion, independent worker progress,
retry delays, source failure, and shutdown before reconnect.

The queue holds at most 1024 distinct pending, delayed, and active keys. Watch
delivery and scans wait for capacity. Each producer holds at most one additional
waiting key. They stop on cancellation and do not evict failed keys or reset
retry delays. This limit is not a production capacity result. If persistent
failures occupy every slot, new keys can remain blocked. Durable retry storage
and complete saturation handling remain open. Identity and database controllers retain their current
scheduling paths. Cross-process leases, fencing, and production recovery bounds
remain open. Four workers do not permit four controller replicas for one scope.

Local checks on 2026-09-09 passed the scheduling regression and the real Gateway
Kubernetes workflow. The real workflow took 219.955 seconds and included
provider persistence, Pod and database restart, former-cluster cleanup, and
reopening cleanup after a late namespace. Controller unit tests passed with race
detection. The compiler pin is recorded in `.stego/compiler-revision`.

The final full application race suite passed with PostgreSQL and Keycloak
required. The acceptance package took 623.857 seconds. It includes the scheduling,
unfinished-claim, and backlog checks. Generation from the pinned remote
compiler reproduced all 70 generated, dependency, and state file hashes. Module
verification and `go vet ./...` passed.

`TestGatewayBacklogLargerThanQueueMakesProgress` seeds 1280 Gateways through the
domain transaction path, including owner grants and deletion events. It drains
those events and restarts the API before the workload controller starts. One
controlled provider remains unavailable. Other provider calls take at least five
milliseconds. The test requires all 1279 healthy cleanups to finish, reads the
first and last retained observations through gRPC, and checks delivery of the
last Gateway's new cleanup event. It also checks the public deleted response.

The previous runtime repeatedly reset discovery and completed only 31 healthy
cleanups during the 30-second check. With generated admission backpressure, all
healthy cleanups finish within the same check. The focused run took 55.917 seconds
including data setup, API startup, and restart. This is a controlled backlog test,
not a measurement of Kubernetes fleet throughput. A queue filled entirely by
persistent failures still needs a storage and recovery policy.
