# State namespace cleanup

The complete Gateway workflow at `a5bf571` observed allocation cleanup pending
at 38.727 seconds after deletion was accepted. Whole cleanup was observed
complete by 40.586 seconds. This exceeds the 30-second target for that test.
The observation does not identify one cause or prove production capacity.

The allocator removed the console state namespace before it requested removal
of the Gateway state namespace. Both have the same prerequisites: Gateway and
Sandbox namespaces must be absent, and SQL and workload cleanup must be
recorded as complete. The domain controller now requests both state deletions
once these prerequisites hold. It does not wait for the first namespace to
vanish before it requests the second deletion.

STEGO still performs ownership checks, conditional deletion, binding removal,
reconciliation scheduling, and telemetry. Hypershell selects the profiles and
prerequisites. A failed first request stops the second request. Cancellation
also stops further requests. A pending first deletion does not hide a failure
from the second request. Finalization still requires both deletions to finish.
No finalizer or grace period is removed.

Regression checks cover each pending state, both pending states, both absent
states, provider errors, cancellation, and recovery in a new controller.
Existing prerequisite, conditional-commit, and finalization checks remain.
Hosted and complete live workflow results are required before qualification.
No latency improvement is claimed from source review alone.

## Hosted checks

Source `8a5e38d` passed its [full hosted check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35497164053).
Independent review confirmed 1,103 core cases in 350 top-level tests. All
1,093 prior cases remain. The ten added cases cover independent state
deletion, cancellation, and controller restart. The browser, console, and
service image checks also passed. Five core fixture tests remain for the
separate live checks. The Kata job remains deferred.

The allocation check passed 23 top-level tests. The adapter check passed
68 top-level tests, and its trace checks passed all 99 required cases.
The [hosted evidence record](state-cleanup-overlap-hosted-evidence.json)
contains the run links, result hashes, counts, and scope of each check.

The complete live workflow has now passed, as recorded below. Hosted results
alone do not establish a latency improvement or production capacity.

The [jshell CPU budget](jshell-capacity-budget.md) is below the current
request total for 100 Gateway servers. The full capacity target needs a
separate suitable test environment or a measured resource profile.

## Complete live workflow

[Live run 35498460152](https://github.com/jsell-rh/hypershell-stego/actions/runs/35498460152)
passed all 11 required tests at `8a5e38d`. The browser workflow took 721.71
seconds. Independent review matched 1,593 source files, all 421 generated
hashes, and the signed f6ebd0b compiler in the admitted test Pod. All four
viewed UI images match the final archive. The nine expected worker instances
exported the required logs, metrics, and traces.

The workflow passed database restart, namespace replacement, access rules,
account creation and token use, and durable allocation finalization. While
the allocator was stopped, REST and gRPC kept the Gateway visible as deleting
after the other cleanup owners finished. Cleanup completed after the
allocator resumed. Independent cleanup found both fixtures absent, the lease
free, and all 32 standing installation resources unchanged. See the
[live evidence](state-cleanup-overlap-live-evidence.json).

The normal deletion sample used 100 accounts created through REST, each with
verified token issuance. Whole cleanup was observed complete by 34.826647259
seconds. All 100 account rows and journals were closed; provider clients and
users were absent; all 100 cleanup success audits were present.

| Observation | Last observed pending | First observed complete |
| --- | --- | --- |
| Account cleanup recorded | 5.422 s | 6.450 s |
| Gateway namespace absent | 14.753 s | 15.780 s |
| Sandbox namespace absent | 23.160 s | 24.245 s |
| SQL and workload cleanup recorded | 25.283 s | 26.312 s |
| Console state namespace absent | 31.645 s | 32.725 s |
| Gateway state namespace absent | 32.704 s | 33.768 s |
| Allocation cleanup and Gateway finalization | 32.738 s | 33.865 s |

The prior whole-cleanup observation was 40.586 seconds. This run observed
34.827 seconds, but allocation cleanup was still pending after 32 seconds.
The 30-second target remains open. The reads are sequential observations,
not exact provider transition times or proof of the cause of each delay.
No finalizer, grace period, ownership check, or completion requirement was
removed to obtain this result.

Independent trace verification first stopped because the copied operator
plan omitted the public Gateway and Sandbox network profile fields. The
actual CI invocation confirmed `public_gateway=true` and
`sandbox_network=false`. The missing fields were restored, the original
records were retained, and all remaining verifiers passed on the same result.
The application source, test run, and verification requirements did not change.

The separate [API run 35499601572](https://github.com/jsell-rh/hypershell-stego/actions/runs/35499601572)
passed all 52 required tests at the same source. Independent review matched
all 1,593 source files, 421 generated hashes, repeated generation, and the
signed compiler bytes in the actual bounded test Pod. The final joint audit
found both fixtures absent, no remaining test allocations or jobs, a free
lease, and all 32 standing resources unchanged. See the
[API evidence](state-cleanup-overlap-api-evidence.json).
