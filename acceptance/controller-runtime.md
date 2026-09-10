Four Hypershell controllers now use the STEGO `controller` component:
Gateway identity, Gateway workload, managed database, and Pod count. Their `Run` methods
supply typed watch and scan adapters, domain actions, and limits. They no longer
implement their own queue, scan scheduler, reconnect loop, or worker shutdown.

STEGO owns the controller lifecycle. Hypershell owns placement, identity mapping,
workload definitions, provider ownership, access policy, and cleanup order.
Gateway events cause a fresh privileged state read. Database deletion uses the
retained replay contract. Moving the runtime does not make a watch event or a
missing read sufficient authority for deletion.

Gateway identity, Gateway workload, and database controllers use the generated
keyed watch runtime with four workers each. They combine duplicate keys, retain changes during actions, and
schedule retries per key. Identity scans repeat after 30 seconds; workload and database scans
repeat after ten seconds. Each action has a 20-second limit. Their queues hold
1024 pending, delayed, or active keys. Scans and watch delivery wait for capacity.

The database controller validates event records before it emits their IDs. Its
provider actions use current retained state. The
[database scheduling check](database-scheduling.md) proves independent cleanup
after API restart. All three controllers have a one-second reconnect delay.
The runtime cancels and joins active callbacks before a new watch session.

Database deletion replay uses generated `ScanStream`. Stream setup and each
receive call have separate 20-second limits. A stalled replay is cancelled so
a later scan can retry. Queue admission can wait without a receive timeout.
Hypershell retains capability checks and record validation. The scanner owns
the item limit and closes the stream on every exit.

These controllers require one active process for each ownership scope. Retained
scans recover failed work and missed deletions. There is no distributed lease,
fencing, or exactly-once write guarantee. Provider actions must tolerate repeated
execution. Each source must respect cancellation and bound its requests.

The Pod count controller uses the generated keyed queue. It holds at most 10,000
keys, including active and delayed keys. Its action limit is five seconds. Retry
delay grows from one to 16 seconds. The runtime combines duplicate keys, retains
changes during a write, and pauses new queue takes during an incomplete baseline.
Hypershell retains Pod classification, count calculation, and cluster ownership.
Its changed-namespace set describes one cache update; STEGO holds pending work.
The [count workflow](sandbox-counts.md) records application evidence.

Service-account recovery also uses this component through `RunSweep`. STEGO owns
its cursor progress, page checks, worker pool, pass deadlines, and group rotation.
Hypershell supplies state groups, storage filters, and recovery actions. The
[account workflow](service-accounts.md) covers late provider effects, expiry,
role limits, restart, and partial-page progress.

Shared storage adapters, durable claims, and distributed fencing remain STEGO work.
The first extractions do not establish that every remaining application helper
is domain-specific. Further changes must remove common application code and pass
an existing application workflow.

The three controller domain race suites passed with the local generated build.
The compiler's generated tests cover resource IDs and typed records, source
failure, queue overflow, recovery, denied access, slow scans, deadlines, and
shutdown. Pinned generation and application static checks passed at compiler
`b7837932b0fc6792cb6859e69a39d7096fff7e60`. Compiler CI passed in run
`34389383086`.

The focused application race suite passed in 95.408 seconds. It includes the
identity workflow (38.22 seconds), stored-grant user login (41.60 seconds),
Gateway recovery IDs (3.21 seconds), database deletion replay (3.29 seconds), and
CLI apply (8.03 seconds). These durations include fixture setup. They are not
production capacity results. CI runs the complete application suite and the
real database, Gateway, and sandbox workload gates.

Full CI on the first migration found missing failure diagnostics in the database,
Gateway, and sandbox workload tests. The tests were not weakened. Generated
clients now supply safe protocol summaries. The application reports Kubernetes
methods and status codes or gRPC codes, without remote messages or response bodies.
The corrected local sandbox workflow passed in 317.874 seconds, including the
cleanup diagnostic check. The three original failure jobs are not counted as
passes. New CI checks the corrected compiler pin and Pod count migration.
