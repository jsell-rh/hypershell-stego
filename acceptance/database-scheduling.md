# Database reconciliation scheduling

The database controller uses STEGO's generated `RunKeyedWatch` with four workers.
Each database ID has at most one active action within a Run call. The queue
combines repeated keys and preserves changes received during an action. Failed
keys retry with delays from one to ten seconds. Events cannot bypass that delay.
The queue holds 1024 pending, delayed, or active keys. Scans and watch delivery
wait for capacity and stop on cancellation.

The watch opens before the recovery scan. A failed watch cancels and joins the
session before reconnect. Every connection starts another recovery scan. Each
action has a 20-second limit. Scans repeat after ten seconds, and reconnect waits
one second. Live list calls also have a 20-second limit. The finite deletion
replay uses STEGO's generated `ScanStream`. Setup, including the capability
header, and each receive call have separate 20-second limits. The scanner
accepts at most 1,000,000 records, then requires EOF. It cancels the stream on
every exit and waits for the active callback to return. The receive timer does
not run while queue admission waits for capacity. There is no total scan time
limit.

Watch and replay records require a known event type and matching resource IDs.
The declared tombstone capability is required. Invalid records, missing
capabilities, and invalid list pages stop the controller. The queue retains only
the validated ID. Each action reads the current retained record, resource
revision, deletion flag, and cleanup owner before provider work.

Current live state selects provisioning. Current deleted state selects cleanup.
A delete-type hint for a live database causes a fresh live-state reconciliation;
it cannot authorize provider deletion. A missing retained record remains an error
for every event type. Failed reads and missing or invalid metadata stop all
provider work. A cleanup observation still requires a current revision and an
explicit API grant.

`TestDatabaseCleanupMakesIndependentProgressAfterRestart` creates and deletes two
databases through REST, drains their events, and restarts the API. The controller
finds their retained records through deletion replay. A controlled provider holds
the first deletion call. The second database must finish cleanup through TLS gRPC
and deliver its new event while the first stays pending. Releasing the first call
permits its completion. Public reads still return 404. The shared helper also
checks Gateway workload and identity cleanup.

The FIFO baseline failed this check in 7.730 seconds including setup. The keyed
controller passed the focused check in 5.16 seconds. Controller tests also verify
current-state authority, conditional writes, denied or missing reads, invalid
recovery input, and cleanup after every supported event type. A false delete hint
cannot delete a live database.

The focused retained-read and three independent-cleanup checks passed in 20.492
seconds. The full PostgreSQL/Keycloak race suite passed, with a 647.391-second
acceptance package run. The real database Kubernetes workflow passed in 76.921
seconds, including deletion replay under C and ICU collations. The complete
Gateway Kubernetes workflow passed in 215.211 seconds. It checks database and
identity setup, access rules, provider persistence, restart, offline deletion,
and repair of late resources. Module verification, `go vet`, and pinned
generation checks also passed.

STEGO owns scheduling, retries, worker limits, watch reconnect, and cancellation.
It also owns finite-stream deadlines, item limits, and closure.
Hypershell supplies protocol validation, replay/list adapters, provider rules,
and field updates. No payload cache, scheduler, or retry map was added to the
application.

Use one active database controller per ownership scope. Cross-process fencing,
durable retry storage, complete queue saturation handling, count-free cursor
storage remain open. A queue filled with persistent
failures can still prevent new keys from entering. Database generations, field
permissions, and provider identity history also remain separate work.

`TestDatabaseReplayIdleLimitRestoresCleanupAfterRestart` creates and deletes a
database through REST, drains its events, and restarts the API. A test adapter
opens a real TLS gRPC replay and confirms its header, then holds the first
receive call until cancellation. The next replay must read retained state,
complete provider cleanup, commit its observation, and deliver the new event.
Deleted state must not reach provisioning. Controller shutdown must join its
workers. The prior implementation failed the 25-second cancellation check;
the failed test took 29.32 seconds, including setup.

With generated `ScanStream`, the focused check passed in 25.96 seconds,
including setup. The first blocked receive was cancelled, and a later replay
completed cleanup and delivered the observation event. The compiler pin is
`9cf5a48d2b7bbf7d32b576a23d87a18b6390977b`, with controller component 1.6.0.

The real database Kubernetes gate passed on 2026-09-10. Provisioning, TLS
access, data and password persistence, foreign namespace denial, offline
deletion, and late-effect cleanup took 73.98 seconds. Deletion replay with C
and ICU collations took 11.59 seconds. The acceptance package took 86.616
seconds. Five stable reconciliations took 72.671 ms and did not change the
Deployment. These results do not establish production capacity.

The first full-suite and Kubernetes attempts stopped without final results.
Their process handles and processes were absent when work resumed. They are
not counted as passes. The Kubernetes figures above come from a new complete
run with the same source.

The complete PostgreSQL/Keycloak race suite then passed on 2026-09-10. Its
acceptance package took 656.062 seconds. This run includes the stalled-replay
check, independent cleanup, access denial, atomic owner grants, event delivery,
and restart. Module verification and `go vet` also passed.
