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
deletion call. The
other Gateway must record completion through TLS gRPC and deliver its event
through the generated runtime. The blocked Gateway must remain pending. After
its provider call is released, it must also complete and deliver its event.
Public reads continue to return 404. The separate Kubernetes gate runs the
actual Gateway provider workflow; it does not measure a large concurrent fleet.

The test failed against the FIFO controller because the blocked call prevented
the second cleanup. It passed after the controller used the generated keyed
watch runtime. STEGO's generated tests separately check 10000 duplicate events,
changes during active work, per-key exclusion, independent worker progress,
retry delays, source failure, and shutdown before reconnect.

The queue holds at most 1024 distinct pending, delayed, and active keys. Overflow
restarts discovery. This limit is not a production capacity result. Repeated
overflow and a backlog of permanently failing keys still need an admission and
fairness contract. Identity and database controllers retain their current
scheduling paths. Cross-process leases, fencing, and production recovery bounds
remain open. Four workers do not permit four controller replicas for one scope.

Local checks on 2026-09-09 passed the scheduling regression and the real Gateway
Kubernetes workflow. The real workflow took 223.813 seconds and included
provider persistence, Pod and database restart, former-cluster cleanup, and
reopening cleanup after a late namespace. Controller unit tests passed with race
detection. The compiler pin is recorded in `.stego/compiler-revision`.

The final full application race suite passed with PostgreSQL and Keycloak
required. The acceptance package took 559.942 seconds. It includes the new
scheduling and unfinished-claim checks. Generation from the pinned remote
compiler reproduced all 70 generated, dependency, and state file hashes. Module
verification and `go vet ./...` passed.
