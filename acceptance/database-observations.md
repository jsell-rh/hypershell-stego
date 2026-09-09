# Database observation baseline

Gateway uses generated resource revisions and observation groups. ManagedDatabase
does not yet use that contract. This gap remains part of the reconciliation work
from Hypershell PR 200.

A temporary PostgreSQL probe tested commit
`371bf78` on 2026-09-09. It used the normal catalog service and a configured
control-plane subject:

1. A platform admin created a deployment database named `observed`.
2. The controller read that record.
3. The admin changed its name to `changed`.
4. The controller submitted `ready` from its earlier observation.
5. The probe required rejection of that stale observation.

The probe failed in 0.14 seconds. The API accepted the write and returned name
`changed` with status `ready`. A serializable transaction for the later write
cannot establish which desired state the controller read. The probe did not run
an external database provider; it isolates the missing commit precondition. It
ran in a temporary checkout and was removed after execution.

The next application gate must use the existing generated revision and generation
contracts for database observations. It must test a change during provider work,
owner and controller authority, event rollback, current status through REST and
gRPC, restart, and regeneration. A fresh observation must confirm an unchanged
status for a new generation. Periodic provider checks must continue after ready.

Database deletion also needs authoritative retained state. The current controller
passes deletion-event data directly to the provider. Durable cleanup ownership,
conditional completion, and cross-process fencing remain separate requirements.
