# Workload progress checks

The workload controller uses STEGO `RunKeyedWatchWithResult`. An exact
`ErrPending` from the workload provider requests another check after one
second. It does not increase the error retry delay. STEGO keeps control of
the queue, worker limits, deadlines, state commit budget, and telemetry.

The application still records pending work as incomplete. It clears an
unverified public address. SQL cleanup must finish before workload cleanup
can finish. Namespace allocation and deletion order do not change.

Only the exact provider value has this meaning. A wrapped or joined error
remains a failure. A failed state write, work deadline, or cancellation also
remains a failure. A pending value from the state commit is not a successful
provider observation. The result contains no scheduled progress check when
one of these failures occurs.

Tests cover pending work, provider and commit failures together, stale
writes, cancellation in work and commit, a late work result, SQL cleanup,
and a workload that is present after a prior completion. Existing restart,
revision, endpoint, and database tests use the new result API.

## Evidence boundary

This change follows the measured 34.826647259-second cleanup at source
`8a5e38d1f7c55b9bb45750f3dfff62dd31a0d4be`. That result did not meet the
30-second target. Its phase observations show namespace and allocation
work after account cleanup. They do not establish the cause of each delay.

Hosted checks and a new live workflow must verify this source. No new
cleanup time or capacity result is claimed here. No local performance test
was run. Kubernetes finalizers, permissions, and grace periods are unchanged.

## Focused checks

At candidate `69b9e29`, independent review verified 71 adapter top-level tests,
including all 15 new workload progress fault cases. The allocation check
passed 23 top-level tests. The trace evidence checks passed all 99 cases.
Provider discovery passed 33 inventory cases and seven provider tests. Journal
recovery passed all 44 required top-level tests.

The generated Gateway console check matched 130 source and configuration files,
the signed compiler, and the built and pulled image records. The image runs as
user 65532 and uses the expected service entry point. See the
[focused evidence](workload-pending-focused-evidence.json).

Full run [35500072508](https://github.com/jsell-rh/hypershell-stego/actions/runs/35500072508)
then passed at the same source. Independent review verified 1,121 core cases
across 353 top-level tests. All 1,103 baseline cases remain, with the 18 new
workload progress cases. The rendered browser, UI, and image checks passed.
The deferred Sandbox job remains skipped. See the [full evidence](workload-pending-full-evidence.json).

No live run was dispatched for this source. The earlier pending-result
candidate failed during its test database cleanup, and the live gate stopped.
Candidate `5f70035` gives fixture cleanup a separate bounded deadline and checks
database absence. It has fresh hosted gates. The original failed result remains
a failure. See the [fixture change and evidence](https://github.com/jsell-rh/hypershell-stego/blob/5f70035868848328bde229d4995a87997c655183/acceptance/database-fixture-cleanup.md).
No new live cleanup measurement or capacity result is claimed.
