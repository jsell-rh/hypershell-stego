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
