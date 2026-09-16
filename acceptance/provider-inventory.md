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

## First passing scope recovery evidence

Run `35103252059`, source `4497b4d`, passed all seven focused application tests
in 5.023 seconds. The stored JSON events contain seven test passes, a package
pass, and no failure events. The before-cursor race now passes in 0.52 seconds.
The last-page race passes in 0.33 seconds, and final-event rollback passes in
0.37 seconds. The omitted-provider recovery test passes in 0.40 seconds.
This proves the composed fixture cases, not the full deployed application.
Evidence is retained in
`/home/jsell/.local/state/stego/runs/gateway-cleanup-20260916/journal-scope-first-result`.

The corrected CI wrapper is running in `35103462998`. The 49-check jshell API
run is `35103602751`, source `b58d9a2`, with compiler `e1c3222`. Its dedicated
Job is `stego-ci/gateway-api-0dbfcfc000fd`. Only one live cluster test is active.
The full application rerun `35103696549` is queued behind `35102064260`; the
older run does not contain scope closure. Keep application main unchanged until
the required full checks pass.

## Save discovered clients before provider changes

Source `22e497d` uses STEGO's bounded client-name query for a specified Gateway.
It still reads each current client and checks exact ownership. A denied query
has no fallback. The empty-Gateway internal list contract remains unchanged.
The small HTTPS test checks query parameters, foreign candidates, invalid IDs,
changed names, and denied requests.

The partial-disable regression failed before provider 0.15.0: no discovered
client had a saved closure when the second disable failed. Compiler `dfc9a1e`
provides `PrepareCloseExisting`. Hypershell now saves all validated candidates
before the first provider mutation. STEGO owns the journal format, encryption,
version checks, writer gate, and irreversible closure rule. Hypershell supplies
the Gateway and account IDs and retains the disable-before-delete ordering.

The regression and query tests passed with the race detector in 1.067 seconds.
Independent recovery of an omitted journal target also passed in 1.038 seconds.
Logs are `prepare-closure-app-focused.log` and
`prepare-closure-omitted-focused.log` in the persistent Gateway cleanup run
directory. Both generated targets have no drift. Full application qualification
for this provider update is pending.

Run `35103462998` also passed all seven SQL recovery checks with the strict CI
wrapper. Its stored JSON contains no failures or skips and includes the package
pass. Evidence is in `journal-strict-wrapper-result`. That run uses the earlier
compiler `e1c3222`; it does not qualify the new preparation operation.

Large legacy inventories still need bounded discovery progress. The current
bulk helper reads the complete candidate list before it saves these records.
A provider deadline during that read can still prevent progress. Saved account
and journal scans are bounded and have checkpoints; provider discovery must
meet the same recovery requirement before this work is complete.

## Complete API gate with scope closure

Run `35103602751`, source `b58d9a2`, passed all 49 required API checks. The
saved JSON has no failures, skips, or missing checks. The acceptance package
passed in 289.928 seconds. The SQL runtime and cleanup metric packages also
passed. The committed output and all three regeneration snapshots are equal.
This includes REST, gRPC, retained cleanup recovery, concurrent journal
registration, and final-event rollback. Reconstruction fixtures do not imply
a database-server restart.

The runner removed its Job, Pods, and fixture resources. An independent check
found no Jobs, Pods, or PVCs in `stego-ci`, and the live-test lease was clear.
Evidence is in `api-scope-result` and
`api-scope-independent-verification.json` in the Gateway cleanup run directory.
The test JSON SHA-256 is
`de8cf288d1d0886b803757804b1ae972341dab0a9906d6f8e7a44792249e122f`.

The earlier queued full run `35103696549` was cancelled before it started.
Full run `35104550272` now targets `550b2b7`, which includes provider closure
preparation. The complete public browser workflow `35104667835` targets the
same source and is the only live cluster run. The earlier API result does not
qualify that later provider change. Application main remains unchanged.
