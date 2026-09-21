# Queue claim result failure

Hypershell selects STEGO compiler `65b18de8` and the common outbox component
version 1.2.1. A row iteration error returns no partial delivery batch. A query
can have stored leases before its result fails. The generated queue keeps those
leases and uses the normal expiry and receipt checks for recovery.

This change preserves the storage notification contract, message identity,
worker behavior, lease duration, attempt deadline, and retry policy. It changes
no Gateway policy or HTTP contract. It does not explain the earlier event
observation timeout after API restart.

Hosted regeneration checked all 429 generated and module files, 420 generated
hashes, and 41 input hashes. Five files changed: the queue, the CLI compiler
identity, and three generation records. The other 424 files were unchanged.
The queue delta matches the released compiler template and keeps the existing
storage contract import and alias. No compiler or application binary ran locally.
See the [generation evidence](queue-claim-generation-evidence.json).

The compiler release passed 30 generated queue cases with real PostgreSQL,
normal lease expiry, stale receipt rejection, and a mutation check. It also
passed 37 compiler packages and both generated examples. That evidence does
not replace application checks. This candidate still requires full application,
image, and complete live workflow review before main acceptance.
