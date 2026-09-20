# Keycloak inventory during cleanup

The provider can remove a client after an inventory list and before the next
read of that client. The Gateway inventory must continue after this specific
not-found result. It must still read the other candidates and check their
current ownership. The provider page length controls the next page request.

A failed list, a denied read, a server error, or an invalid response still stops
the scan. The scan returns no partial result on these failures. It performs no
write. A short or empty list does not prove that cleanup is complete. Retained
account records and cleanup journals remain required.

STEGO already supplies the common bounded client, typed not-found result, and
cursor rules. This change uses that result in the Hypershell inventory view.
Gateway selection and ownership rules remain in Hypershell. The common
single-client read still returns not-found to its caller.

The new test covers both Gateway and global inventory. It removes candidates
at each position, removes a full page before a second page, and checks that
request errors and invalid current representations stop the scan. The hosted
provider check also runs the real late-creation recovery test. The original
recovery test is unchanged.

The failed full run is recorded in `keycloak-inventory-failure-evidence.json`.
Focused checks, full hosted checks, and the live application workflow must
pass on the corrected source before it can replace the current main branch.

Source `a5bf5717448086b093f5cac4cab0b516b706353a` passed all five hosted
checks. Independent review verified 1,093 full core cases across 347 top-level
tests, including all 982 baseline cases. The rendered browser, web console,
and service image checks passed. The five expected fixture skips are unchanged.

The focused provider check passed all 33 inventory cases and all seven required
real-provider tests, including the unchanged late-creation recovery test.
Journal recovery passed all 44 required tests. Allocation and adapter checks
passed, including 99 trace and Pod-guard cases. Source, archive, compiler, and
test-log identities are recorded in `keycloak-inventory-hosted-evidence.json`.

The complete live Gateway workflow passed on the same source. All 11 required
tests passed. Independent checks verified 1,589 source files and 421 generated
file hashes, the signed compiler package, the admitted Pod limits, all four
viewed screenshots, and controller log and trace pairs from all nine expected
worker instances. The workflow includes database restart, namespace replacement,
access denial, and durable deletion while the allocator is stopped.

The test created 100 accounts through REST and verified their token issuance.
Final cleanup closed all 100 account records and journals, removed the provider
clients and users, and retained the other Gateway and supplied database.
The observed whole-cleanup upper bound was 40.586 seconds. Sequential reads
still observed allocation cleanup pending at 38.727 seconds. The 30-second
target is not met by this observation. Account cleanup was first recorded
complete at 5.163 seconds; this does not prove whole-Gateway cleanup.

Both test fixtures and their allocations were absent after the run. The shared
lease was free, and all 32 standing resources were unchanged. The record is in
`keycloak-inventory-live-evidence.json`. The separate API test remains required.
Production capacity is not proved. Live Kata execution remains deferred by the
user.
