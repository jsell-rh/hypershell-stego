# Account cleanup retry policy

The private provisioner retains fixed RPC status classes for temporary
state-store failures. It removes the original provider message and status
details. The cleanup client marks only aborted, unavailable, exhausted-capacity,
and deadline failures as eligible for a bounded immediate retry. A canceled,
denied, or unknown result has no retry marker. Public error identity and text
remain unchanged.

Source `7333332` passed all 38 required journal checks in run `35473973149`,
including all 33 client and server status cases. The original six-account serial
restart fixture and the 32-account parallel restart fixture also passed. There
were no failed or skipped tests. See the [policy evidence](cleanup-retry-policy-evidence.json).
The merge with current main changes acceptance records only.

The signed STEGO compiler `ee348b8` supplies retry execution. Hosted run
`35474624738` regenerated all three modules. Independent checks matched the
source archive, compiler signature record, all three drift checks, and all 420
generated files. Fourteen generated or state files changed. See the
[generation evidence](cleanup-retry-generation-evidence.json).

Account cleanup now permits two attempts, with a 25-millisecond delay, only
when the private provider error contains the retry marker. Cancellation blocks
an immediate retry. A bare local deadline, denied request, unknown error, or
inventory-pending result does not permit it. The common runtime also rejects
scan-contract and window-limit errors. Each attempt retains the full
750-millisecond action budget. The two-second work budget, one-second commit
reserve, page limits, and worker settings are unchanged.

The application supplies error selection. STEGO owns retry execution, context
release, delay, time reserve, key order, and saved failure rules. The action
must remain safe to repeat after an uncertain response. Retained cleanup,
provider inventory, late-effect checks, and scope closure remain required.

The application test covers both serial and parallel use. It checks selected
statuses, exhausted attempts, denied and unknown errors, cancellation, local
deadlines, and scan-contract failures. The new application gate, full workflow
checks, and capacity measurement remain pending. The latest complete capacity
result still fails the 30-second target.
