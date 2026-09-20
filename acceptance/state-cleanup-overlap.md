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
