# Common controller runtime compiler

Candidate `69b9e29` selects signed STEGO compiler
`c515f2208c27cfab51db0e9e7947089a1019bb01`. The compiler supplies telemetry for
its public `controller.Run` entry point. Hypershell continues to use the
common keyed and sweep runners. Direct public Run behavior is covered by the
compiler tests; application checks do not substitute for those tests.

The same candidate contains the [workload progress change](workload-pending-result.md).
That adapter maps exact provider pending results to the existing common
scheduler. It does not add an application queue, retry engine, or telemetry
runtime.

## Verified generation

[Generation run 35499961261](https://github.com/jsell-rh/hypershell-stego/actions/runs/35499961261)
used the exact pinned source. Independent review matched the signed compiler
installation and version to the published release. It checked all 420 generated
source and module files. Only five files changed: the controller runtime, CLI
compiler version data, and three generation records. The runtime bytes match
the exact compiler template. Registry and project digests were independently
recomputed from the selected source bytes.

All 126 Gateway console source and module files stayed identical. The existing
immutable Go module reference can remain. The full application check must
verify its selected module again. This resolves the conservative module-refresh
note in the initial generation record.

The first review stopped on a registry content digest change. The controller
metadata version changed from 1.25.0 to 1.25.1. A second verifier computed both
old and new digests from the pinned common registry and application declarations,
then checked complete state equality. The original failure is preserved. No
output or verification requirement was removed.

The pin-only commit had three failed checks because committed output still
used the earlier compiler. Those are recorded failures, not application passes.
The verified output was then committed separately. Fresh checks use `69b9e29`.
See the [generation evidence](controller-run-compiler-evidence.json).

## Remaining proof

The full application suite passed at `69b9e29`, including 1,121 core cases
and the rendered browser check. See the [full evidence](workload-pending-full-evidence.json).
The live workflow remains required. No new cleanup time or capacity result
is claimed. The previous complete workflow and its
separate API result remain in [state cleanup evidence](state-cleanup-overlap.md).
The measured 34.827-second cleanup still exceeds the 30-second target. Live
Kata isolation and full production capacity remain unverified.
