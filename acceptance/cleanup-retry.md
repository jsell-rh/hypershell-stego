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
deadlines, and scan-contract failures. Source `b450f11` passed all 39 journal
checks in run `35474686206`, including all 33 status cases and all 22 retry
cases. Both restart fixtures passed without changes to the original serial
fixture. There were no failures or skipped tests. See the
[application evidence](cleanup-retry-application-evidence.json).

The regenerated Gateway console passed its source and image gate. Independent
checks matched all 130 selected source and module files, compiler verification,
repeated generation, image binary, published digest, and non-root configuration.
Its module is unchanged in candidate `51b2bb2`. The root application now selects
that module through an exact source revision. See the
[console evidence](cleanup-retry-console-evidence.json) and
[module evidence](cleanup-retry-module-evidence.json).

The browser job passed in full run `35474805484`. Three browser runtime instances
each supplied all eight startup signal groups, with correlated logs and spans,
metrics, and active-state records. Both screenshots were reviewed. They show a
provisioning view with a command loading placeholder and an empty service-account
list after deletion. These hosted fixtures do not prove live Gateway readiness.
See the [browser evidence](cleanup-retry-browser-evidence.json).

The ninth capacity run, `35475425102`, completed account cleanup in 61.55 seconds.
It failed the unchanged 30-second target. All 100 selected account rows and
journals closed. All 100 provider clients and users were removed. The 9,900
background rows and 10,007 other provider clients stayed unchanged. Source,
compiler, binary, resource-limit, and test-cleanup checks passed. See the
[ninth capacity evidence](capacity-ninth-evidence.json).

The fixture matches the eighth run, which took 44.97 seconds. This comparison
does not show a timing improvement. One run does not establish the cause of the
difference. The first saved scan contains a failure; a later complete scan is
required before scope closure. The first observation of all 100 closed rows was
at 37.34 seconds. The scope closed at 61.25 seconds.

Account recovery repeats deletion for retained deleted rows. Gateway recovery
also checks retained rows and journals. A closed provider journal still checks
the provider for late changes. The trace groups show many repeated deletion
calls, but do not identify which recovery path made each call. These checks
must not be removed without proof that the remaining path covers late changes,
restart, and retained records. The next source review will examine ownership of
these repeated checks before another capacity run.

The full check passed at source `51b2bb2` in run `35474805484`: 332 top-level
tests and 937 cases passed. All 816 baseline cases remain covered. The five
conditional exclusions are unchanged. All 22 retry cases and both restart
fixtures passed. Browser, console, and image jobs passed. The live Kata job
remains deferred. See the [full evidence](cleanup-retry-full-evidence.json).

The later cleanup-owner candidate has separate checks. The full retry result
does not qualify that later source. Account cleanup is one part of Gateway
deletion; its measurement does not prove complete workload and database cleanup
within the target.

The cleanup-owner change now has a passing account capacity result at `4ea3e1e`.
Cleanup took 21.2756 seconds in run `35476517343`, with the same fixture and
limits as the ninth run. All selected identities closed and all background
identities were preserved. The [tenth result](capacity-tenth.md) states the
scope and remaining limits. The full application gate remains active.
