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

Common STEGO candidate `d205073` supplies retry execution. It retains complete
action time budgets, cancellation, key order, saved cycle failures, and
conditional checkpoint saves. It requires an explicit application predicate.
The application retry option is not yet enabled. Compiler release qualification,
regeneration, application checks, and a new capacity measurement remain required.
The latest complete capacity result still fails the 30-second target.
