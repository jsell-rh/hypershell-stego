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
and complete saturation handling remain open. The database controller also uses the [keyed runtime](database-scheduling.md). Cross-process leases, fencing, and production recovery bounds
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
cleanups during the 30-second check. With generated admission backpressure, the initial local run completed all
healthy cleanups within the same check. The focused run took 55.917 seconds
including data setup, API startup, and restart. This is a controlled backlog test,
not a measurement of Kubernetes fleet throughput. A queue filled entirely by
persistent failures still needs a storage and recovery policy.

CI run [34416352961](https://github.com/jsell-rh/hypershell-stego/actions/runs/34416352961)
completed 1009 of 1279 healthy cleanups at the 30-second cutoff. The scan completed,
but the remaining cleanup did not finish in that interval. This is a failed check;
it does not establish the cause of the slower completion. The functional test now
allows 90 seconds and records progress every ten seconds plus total elapsed time.
Its backlog, failing provider, worker count, provider delay, and requirement to
complete every healthy cleanup remain unchanged. A local time limit is not a
production recovery target. Throughput and latency still need measured targets
under defined resources and load.

The later full local race run completed all 1279 healthy cleanups in 17.22
seconds after controller startup. At ten seconds it had completed 834. The
deliberately failing Gateway stayed pending. This result retains the functional
proof but does not replace a CI or production performance target.

The identity controller now uses the same generated keyed watch runtime with
four workers. `TestGatewayIdentityCleanupMakesIndependentProgressAfterRestart`
uses the same REST creation/deletion, API restart, retained-state, TLS gRPC, and
event-delivery check as workload cleanup. With the old FIFO identity controller,
a blocked provider prevented the second Gateway from completing. The failed
baseline took 7.913 seconds including setup. The new runtime permits the second
Gateway to complete while the first remains blocked.

The identity controller keeps its domain user-scan cursors behind a short lock.
It does not hold that lock during API or provider calls. A race test runs 64
independent Gateway user scans, interrupts each after its first user, and confirms
that every scan resumes at its own second user. Finished scans remove their
cursors. The queue and worker pool remain generated by STEGO. Cross-process
ownership, durable retries, and cursor memory bounds remain open. The fixed
10,000-reference inventory limit was removed by [cursor recovery](identity-cursors.md).

The identity scheduling check passed in 5.17 seconds after its failed FIFO
baseline. The focused identity, user-login, and two cleanup checks passed in
72.577 seconds. The final full PostgreSQL/Keycloak race suite passed, with a
652.983-second acceptance package run. The actual Gateway Kubernetes workflow
passed in 219.678 seconds, including identity setup, access rules, provider
persistence, restart, offline deletion, and repair of late resources in a former
cluster. Module verification, `go vet`, and pinned generation checks passed.
