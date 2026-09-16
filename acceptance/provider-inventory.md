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
