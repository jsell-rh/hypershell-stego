The identity worker must stop with one safe failure record if its Run callback
panics or calls `runtime.Goexit`. Its monitor must receive a result and close
its listener. A normal replacement must repair the retained Gateway identity.

`checkIdentityWorkerRunAborts` builds a temporary overlay around the generated
identity worker main. The real domain callback still supplies provider setup,
watch and scan handling, reconciliation, and cleanup. The test first changes
OIDC state while the worker is absent. The fault worker repairs the state
against real Keycloak. On shutdown, its outer callback aborts after domain
cleanup. The test requires exit code 1, one fixed failure record, and no private
panic value, bearer token, or stack text. A final healthy worker repairs another
state change and exits normally.

The check runs in the existing identity workflow and the Kubernetes Gateway
workflow. In the cluster check, the fault workers are race-enabled test
processes that use the separate API and Keycloak Pods. The ordinary worker
Deployment still has its own Pod replacement and OTLP checks. Test processes
use local signals only, so they do not alter the two-instance deployment count.
The fault exists only in a temporary Go build overlay. The committed worker has
no fault environment setting or fault route.

This boundary covers the goroutine that calls Run. It does not cover goroutines
started by domain code or controller actions in other goroutines. A callback
must still stop after context cancellation. The broader controller failure and
production requirements remain open.

The previous compiler, `cb0326d`, failed this check on jshell on 2026-09-11.
The ordinary deployment and identity repair stages completed. The callback
panic then exited with code 2 and exposed the private panic value. The test
failed after 108.05 seconds. The failed Job, initial source archive, and logs
are retained in `/tmp/stego-service-results.484Kcd04`. Its namespace was deleted,
and private fixture files were removed. That run did not reach the Goexit case.

The first fixed-pin run built both images but did not produce a test completion
record. The wrapper collected files before the remote command finished, then
stopped the Pod. This run is invalid as application evidence. Its partial files
are in `/tmp/stego-service-results.WlWerNmV`. The namespace was deleted.
The Job now owns the build and test process. The wrapper waits for an atomic
exit record and checks Pod state after observation errors. A closed connection
alone cannot mark work complete. The Job retains its 30-minute time limit.

The next run reported a passing application test in 117.59 seconds, including
both abort modes and healthy recovery. Its wrapper source was changed while
the wrapper was active. The shell skipped evidence collection and removed the
namespace while the Job awaited collection. This run does not establish Job
completion or the retained post-test hashes. Its partial record is in
`/tmp/stego-service-results.IxJ1D1qg`. The next check uses a frozen source copy.

The frozen-source run passed on jshell on 2026-09-11 with compiler
`bbfacd13a018261b5c9e11041ec55e62fd60cbc3`. Application commit `1a5ae98` records
the runtime adoption and fault checks. The test took 114.76 seconds; the
race-enabled package took 115.801 seconds. Both abort modes passed, followed
by repair from the healthy worker. The full Gateway deployment, access, event,
restart, and telemetry checks also passed.

The Job reached `Complete` with exit code 0. All 117 generated, state, and
dependency hashes matched across both generation passes, the post-test check,
and the checkout. The generated archive also matched the checkout. The saved
Pod log matched the independently collected log. All 495 files in the frozen
source copy still matched its initial archive. Namespace deletion and removal
of private fixture files were verified. The full record is retained in
`/tmp/stego-service-results.b23OdptE`.

The source snapshot predates the input-inventory test correction in `6c8113a`.
That correction changes no runtime or generated input. Its focused contract
test passed locally. [Full variant CI for that correction](https://github.com/jsell-rh/hypershell-stego/actions/runs/34628862977)
is a separate check. The [compiler CI](https://github.com/jsell-rh/stego/actions/runs/34625915955)
passed, including race tests and the vulnerability check.
