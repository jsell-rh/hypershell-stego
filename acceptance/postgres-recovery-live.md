# Live workflow with the recovery compiler

Consumer `d24e9da` and signed compiler `3be6bce` passed the
[complete live workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35503929607).
Independent verification checked all 11 required tests, 1,608 source files,
421 generated file hashes, repeated generation, and the compiler bytes in the
admitted test Pod. The Job had CPU, memory, and time limits. See the
[evidence record](postgres-recovery-live-evidence.json).

The workflow passed Gateway creation, owner grants, filtered lists, denied
requests, event delivery, REST and gRPC access, and restart recovery. It also
passed namespace replacement, SQL privilege denial and repair, provider outage
and recovery, encryption checks, session renewal, and logout. PostgreSQL restart
used the same fixture Pod. This does not test RDS failover.

Logs, metrics, and traces passed. The trace check covered all nine expected
controller instances. Separate reconciliation, scan, and cleanup traces were
verified. All four saved browser views passed visual review. The final artifact
images matched the images reviewed from the test Pod.

Deletion remained visible through REST and gRPC while the allocator was paused.
The Gateway did not finalize before allocation cleanup. It completed after the
allocator resumed. The normal deletion sample created 100 service accounts
through REST and verified their tokens. All selected accounts, journals,
provider clients, and provider users were removed or closed as required.

Complete cleanup was observed at 31.373211219 seconds after the accepted
response. Finalization was first observed at 30.770735066 seconds. Account
cleanup was first observed at 6.291216493 seconds. All 15 stage checks passed,
with no observed regression. These are sequential read observations, not exact
transition times. They do not prove the 30-second target. Namespace status and
allocator logs do not establish the cause of the delay.

Independent cleanup checks found both test fixtures absent, the shared lease
free, and all 32 standing resources unchanged. This result does not prove
100-Gateway capacity, live Kata isolation, or the remaining enterprise scope.
The separate API check is pending.

The first artifact verifier stopped because its copied expected digest still
selected the previous compiler. Its source revision and package paths already
selected `3be6bce`. The corrected verifier checked the current published binary,
consumer pin, and actual test Pod record. All other assertions were retained.
The same saved workflow artifacts then passed. No live test was repeated.
The evidence record retains the old and new digests, script hashes, and error
log hash. The original scripts, logs, and failure states remain in persistent
operator records.
