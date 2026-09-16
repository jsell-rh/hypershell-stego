# Provider inventory recovery

The deletion candidate does not yet prove safe completion after a partial
provider list. The regression
`TestGatewayCleanupDoesNotForgetJournalAfterProviderOmission` failed against
source `fd6371e` in 0.02 seconds. It uses one legacy orphan, a verified TLS
provider fixture, and sealed journals retained across client reconstruction.
It does not claim a database or process restart test.

The first deletion attempt saves the client's closure journal, then receives a
provider failure. The next client instance receives an empty list even though
the orphan still exists. Bulk Gateway cleanup returns success. A direct cleanup
with the retained ID then removes the orphan. The failure shows that known
journal IDs must remain an independent recovery source. A shorter or filtered
provider query does not fix this case.

Keep this regression failure as evidence. Its log is
`/home/jsell/.local/state/stego/runs/gateway-cleanup-20260916/provider-journal-omission-probe.log`.
The test is on `codex/provider-inventory-20260916`; it is not a qualified release.

STEGO must provide bounded enumeration of resource-state keys by entity and
scope. The enumeration must expose no ciphertext, use a stable key cursor, and
validate bounds before database access. Hypershell selects the Gateway scope
and applies account cleanup policy. Recovery must check both retained account
rows and journal IDs before it reports account cleanup complete. Provider
inventory remains necessary for legacy clients with no saved journal.

Required tests include a missing account row, omitted provider list entries,
failed-item retry with independent later progress, a page boundary, source and
checkpoint conflicts, and final-event rollback. Repeat the real provider and
complete Gateway workflows after the fix. The earlier passing workflow tests
do not cover this omission case.

## Candidate recovery change

The candidate now uses STEGO's `ResourceStateKeyReader` and
`SequenceCursorSources`. One durable cycle visits retained account rows and
saved account journal IDs. Hypershell selects the exact Gateway scope and
supplies the account policy. STEGO owns cursor encoding, source transitions,
page bounds, validation before effects, and checkpoint failure retention.
A changed cycle input version resets older account-only cursors safely.

The original failure log remains unchanged. The provider-only test is now
`TestGatewayInventoryRequiresIndependentJournalRecovery`: it shows that
inventory can omit a saved client and that the sealed identity still permits
recovery. It passed locally with the race detector in 1.043 seconds. This is
not application completion evidence.

`TestGatewayCleanupRecoversJournalOmittedByProvider` composes the application
service, generated PostgreSQL storage, encrypted journals, and generated
provider lifecycle. Its HTTPS provider injects a deletion failure and then
omits the client. The test requires cleanup to remain incomplete during the
failure, and then to remove the client through its saved journal after store
and client reconstruction. It does not claim a process or database-server
restart. `TestGatewayJournalCleanupRecoveryKeepsPageCheckpoint` checks 101
journal-only IDs across reconstruction. These database tests must pass in CI.
The jshell API gate now requires both tests, for a total of 46 required checks.

Compiler and application qualification remain pending. Do not promote this
candidate to application main until the composed tests and full workflow
checks pass. Provider inventory query adoption and bounded progress for large
legacy inventories remain open work.
