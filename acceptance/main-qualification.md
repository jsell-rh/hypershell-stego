# Main qualification

All 11 automatic checks passed at exact source `0aa8f0d`. Their saved results
passed independent checks. See the [gate record](main-qualification-evidence.json).
Documentation commits after that source do not change the tested runtime.

The [API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35479995493)
passed all 52 required tests. The checks cover atomic Gateway and owner/event
creation, rollback, filtered access, REST and gRPC, typed SDK and CLI behavior,
watches, durable events, deletion, recovery, SQL ownership, and credential
rotation. All 1,548 source files and 421 committed, repeated, and post-test
generation hashes matched. The actual compiler bytes in the bounded Pod match
the signed package. The Job had a 1,200-second deadline and no retry. See the
[API result and cleanup](main-api-evidence.json).

An independent read confirmed removal of both live test workloads, allocated
resources, and browser fixtures. The API Job, Pods, and labeled private
configuration objects are absent. The shared lease was free. All 32 standing
browser installation objects matched the adopted configuration. These checks
made no cluster changes.

The full hosted suite passed 950 core cases across 334 top-level tests and
retained all 816 baseline cases. The five core exclusions still require their
separate fixtures. The policy-plan job was not selected by this push. The Kata
Sandbox job remains deferred. These explicit exclusions are not passing tests.

The main browser workflow passed all 11 required tests. Its complete
100-account Gateway cleanup upper bound was 53.9497 seconds. This does not
prove the 30-second target. The account-only capacity result of 16.8819 seconds
does not include workload cleanup. The next live test will record each cleanup
phase before a performance change is selected.

This qualification does not close the full enterprise goal. Complete production
capacity, live Kata isolation, RDS failover, whole-database rollback detection,
cross-process fencing, backup and restore, full application parity, and the
remaining compiler and runtime requirements still need their own evidence.
