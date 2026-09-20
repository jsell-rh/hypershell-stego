# Main qualification

All seven automatic checks passed at exact source `e9b9bf9`. Their saved
results passed independent checks. See the
[current gate record](allocation-main-evidence.json). Documentation changes
after that source do not change the tested runtime. The earlier qualification
at `0aa8f0d` remains in [its saved record](main-qualification-evidence.json).

The [API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35486475480)
passed all 52 required tests. They cover atomic Gateway and owner/event
creation, rollback, filtered access, REST and gRPC, SDK and CLI behavior,
watches, events, deletion, recovery, SQL ownership, and credential rotation.
All 1,570 source files and 421 committed, repeated, and post-test generation
hashes matched. The compiler bytes in the bounded Pod match the signed package.

The [browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35486475436)
passed all 11 required tests. Its source and 421 generation hashes matched.
The checks cover login, Gateway creation, grants, denied requests, filtered
lists, REST and gRPC, events, restart, SQL failure recovery, account use,
telemetry, and deletion. Four fresh images passed visual review. The deleting
Gateway stayed visible through REST and gRPC while the allocator was stopped.
Final deletion required removal of both retained state namespaces after resume.
All 24 account observations and all 15 cleanup phase checks passed.

The full hosted suite passed 963 core cases across 337 top-level tests and
retained the earlier cases. Focused allocation, adapter, real-provider, and
journal checks also passed. The five core exclusions still require their
separate fixtures. The Kata Sandbox job remains deferred. An exclusion is not
a passing test.

Independent cluster reads confirmed removal of both test workloads, allocated
resources, and private fixtures. The shared lease was free. All 32 standing
installation resources matched the adopted configuration. The audit made no
cluster changes.

Complete normal cleanup of one Gateway with 100 accounts took 58.2276 seconds.
The 30-second target remains open. The account cleanup flag returned to pending
14 times; the separate rescan fix is still under test. These results do not
prove production capacity, live Kata isolation, RDS failover, rollback
detection, distributed fencing, backup and restore, or full application parity.
The enterprise goal remains active.
