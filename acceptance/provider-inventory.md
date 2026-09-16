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

The candidate source is `d1ec34c140e3fef91af077a7ab027ca7b8cc21d3`, with compiler
`20d6e2e`. Application CI run `35102064260` is in progress. The combined common
runtime is queued in STEGO CI run `35101860805`. The acceptance package compiles;
its database tests have not yet returned results. The active CNPG run
`35100459235` uses the earlier deletion candidate and cannot qualify this fix.

Before release, also test a journal registration that occurs during a scan.
Key pages are not a snapshot. Completion must not miss a new key before the
saved cursor. Current application checks cover a saved journal that exists
before the recovery cycle; they do not yet prove this concurrent case.

## Concurrent registration failure

Focused CI run `35102352727`, source
`1663e167bee27b39c9af5ca19d86c4da9f08275f`, returned four passing tests and one
failure in 2.669 seconds. Retained account retry and page continuation passed.
Journal-only page continuation passed. The composed omitted-provider test
passed in 0.31 seconds with generated PostgreSQL storage and encrypted journals.

`TestGatewayJournalRegistrationDuringScanPreventsCompletion` failed in
0.36 seconds. After the first 100 IDs were saved in a checkpoint, the test
inserted an earlier ID. The reconstructed service visited the remaining old ID
and reported completion with only 101 of 102 saved IDs visited. This is a real
false-completion defect. The test uses a recording provider and empty state
records to isolate key coverage; it does not claim a provider process race.

The result is retained in
`/home/jsell/.local/state/stego/runs/gateway-cleanup-20260916/journal-race-baseline`.
A stable key cursor is insufficient. STEGO needs a revision for each scoped key
set and an atomic operation that closes new-key registration after a successful
scan. Hypershell must commit that guard and its accounts cleanup result together.
The existing application candidate remains unqualified.

## Scoped closure candidate

The candidate now loads STEGO's scoped key-set revision into the durable cycle
input version. A new key makes the next pass start at the beginning. After the
scan and provider inventory succeed, one transaction conditionally closes the
scope and records accounts cleanup. A key accepted during that pass makes the
closure fail. A committed closure rejects later new keys in the database while
existing records remain writable for repeat cleanup.

Compiler `e1c3222` provides the common storage guard. The application selects the
ServiceAccount scope for this Gateway. It does not implement a counter, paging
protocol, or database lock. The acceptance package compiles; SQL qualification
is pending.

The focused CI gate now requires seven tests. In addition to the original five,
`TestGatewayJournalAddedAfterLastPagePreventsCompletion` inserts a key during
provider inventory, after the final page. `TestGatewayJournalClosureRollsBackWithFinalEvent`
requires event failure to roll back scope closure, the cleanup observation, and
finalization. It then adds another key, retries cleanup, and checks one final
event. The jshell API gate now requires 49 distinct checks.
