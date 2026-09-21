# Queue claim result failure

Hypershell selects STEGO compiler `65b18de8` and the common outbox component
version 1.2.1. A row iteration error returns no partial delivery batch. A query
can have stored leases before its result fails. The generated queue keeps those
leases and uses the normal expiry and receipt checks for recovery.

This change preserves the storage notification contract, message identity,
worker behavior, lease duration, attempt deadline, and retry policy. It changes
no Gateway policy or HTTP contract. It does not explain the earlier event
observation timeout after API restart.

Hosted regeneration checked all 429 generated and module files, 420 generated
hashes, and 41 input hashes. Five files changed: the queue, the CLI compiler
identity, and three generation records. The other 424 files were unchanged.
The queue delta matches the released compiler template and keeps the existing
storage contract import and alias. No compiler or application binary ran locally.
See the [generation evidence](queue-claim-generation-evidence.json).

The compiler release passed 30 generated queue cases with real PostgreSQL,
normal lease expiry, stale receipt rejection, and a mutation check. It also
passed 37 compiler packages and both generated examples. That evidence does
not replace application checks.

The candidate passed 1,294 core cases, all five restart cases, 11 real provider
test roots, and seven image reviews. The complete live workflow passed all 11
required tests in 533.05 seconds. Review checked 1,694 source files, 430 generated
hashes, exact compiler bytes, REST and gRPC access, denied requests, event
delivery, restart, telemetry, four browser views, and cleanup. All seven evidence
readers completed without error. See the
[application evidence](queue-claim-application-evidence.json) and
[browser evidence](queue-claim-browser-evidence.json).

Cleanup removed the test resources, released the shared Lease, and preserved
all 32 standing resources. Normal cleanup with 100 accounts took an observed
32.26 seconds. The 30-second target remains open. These checks accept the queue
change. They do not establish production capacity, live Kata isolation, RDS
failover, or the cause of the historical event timeout. Exact main push checks
have separate results.
