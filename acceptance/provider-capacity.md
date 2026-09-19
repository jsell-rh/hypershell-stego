# Real provider account cleanup capacity

The production target is 100 Gateways per instance, with 100 service accounts
per Gateway. Cleanup should complete within 30 seconds after HTTP 202. These
numbers are targets. They are not application limits.

The `Real provider account capacity` workflow tests the account part of cleanup.
It uses the pinned Keycloak image in production mode with a separate PostgreSQL
database. The API uses another PostgreSQL database. Provider HTTPS verifies the
test certificate. PostgreSQL plaintext is limited to an explicit test setting
on the literal loopback address.

The fixture creates 100 Gateway records. Four bounded setup workers create 9,900 provider clients,
service users, and Gateway role grants for 99 background Gateways. The fixture
then seeds their account rows. These accounts supply storage and provider load. The test does not qualify their
creation through the application, token policy, or encrypted journal state. The fixture sets both API quota values to 100. The selected Gateway's 100
accounts must pass REST creation, the generated gRPC provisioner, the common
Keycloak client, and encrypted state storage.

After setup, REST DELETE must return 202 and block new accounts. The generated
runtime must finish account cleanup without a test call to its recovery method.
The timer ends only after the test confirms all of these results:

- The provider state scope is sealed.
- All 100 selected clients and their service users return 404.
- All 100 encrypted journal records authenticate and retain closed identities.
- All 100 account rows are deleted and inactive.
- All 100 cleanup success audit records are present.

The test then compares every other provider client and all 9,900 background
account rows with their saved state. The result contains only counts, timing,
and hashes. It does not contain tokens, client credentials, or journal data.

The hosted CI test process and its API and provisioner children share one CPU,
1 GiB of memory, no swap, and a 256-process limit. Keycloak has two CPUs, 2 GiB,
no swap, and 512 processes. PostgreSQL has one CPU, 512 MiB, no swap, and 256
processes. The test checks its Linux control group before it starts. The test
has a 15-minute limit. CI stops the test unit and removes only provider
containers with this run's labels. No privileged container is used.

Compilation and image download occur before measurement. The record includes
the source commit, source archive hash, compiler pin, Go version, binary hashes,
process exit code, test output, and cleanup result. The test has no race
instrumentation. The separate provider discovery gate retains its race and
restart checks.

This first sample does not qualify total Gateway cleanup. It does not run 100
Gateway Pods, remove their databases or identity clients, measure concurrent
Gateway deletion, or prove a larger installation. A failed timing result must
remain visible. The two-minute cleanup observation limit permits a slow run to
save its completion result; it does not replace the 30-second target.

The first run, `35459418645`, failed during setup. The single realm import did
not finish within eight minutes. No REST account was created, and no cleanup
time was measured. Source and binary hashes matched, and CI confirmed cleanup.
The fixture now uses separate provider API transactions with four workers and an
eight-minute setup limit. This change does not raise the resource limits or
reduce the account count. See [the result record](provider-capacity-evidence.json).

The second run, `35460101439`, created all 9,900 background clients, users, and
role grants. It then created ten accounts through REST. The eleventh request
returned 429 because the former creator quota was fixed at ten. This is an
application policy limit, not a request-rate limit. No cleanup was measured.
The quota values are now operator settings; the original defaults remain.
Tests check invalid settings, both default quotas, and concurrent reservations
with smaller configured limits. The capacity gate sets each quota to 100.

The third run, `35460485398`, created all 100 selected accounts through REST.
The fixture confirmed 100 Gateway rows, 10,000 active account rows, and all
10,000 expected provider clients. REST DELETE returned 202 and blocked a new
account. The provider state scope remained unsealed through 120.0735 seconds.
The 30-second target failed. The record has no completion time. The test stopped
before its final provider absence, closed-journal, and background preservation
checks, so it does not prove those results. Test resource cleanup passed, and
the source archive and all binary hashes matched.

Quota and journal regression run `35460485337` passed 32 top-level tests with
race checks and no failure or skip. The quota policy was then copied to main at
`034b46b`; all seven changed files match the tested candidate. Main's complete
workflow checks remain separate. The next capacity change must measure progress
and retry delay before it changes the recovery cadence or work budgets.

The next sample enables authenticated OTEL delivery over verified TLS for the
API and provisioner. The test receiver retains fixed operation and outcome
classes, counts, and durations in ten-second buckets. It keeps at most 1,024
trace groups. It counts log and metric records, then discards their contents.
Raw spans, credentials, request bodies, and provider responses are not saved.
Every two seconds, the test records closed account counts and durable checkpoint
versions, flags, and cursor hashes. These checks share the existing test limits.
Full trace sampling adds work, so this diagnostic sample is separate from the
third sample. It does not change the 30-second target or recovery settings.

The fourth run, `35461469012`, supplied 59 progress samples and 142 trace groups.
No trace group was dropped. All 100 account rows were closed by the first sample
at 43.1161 seconds. The provider scope remained unsealed at
120.0213 seconds. Completed checkpoints 5 and 8 retained failure
flags and restarted their scans. The provider inventory checkpoint stayed at
version zero. The trace record includes seven two-second controller timeouts.
It also shows repeated reads and deletion checks for retained closed accounts.
It does not prove final provider absence or background preservation.
Source and binary hashes matched, and test resource cleanup passed.

STEGO `58a3bcc` adds an action time reserve to the common scan API.
It stops a pass before a new action if a full action budget is not available.
Real errors remain in the saved cycle. All six branch and main compiler jobs
passed, including 34 packages with race checks. The immutable compiler release
passed signature and source checks before consumer selection.

Hosted generation run `35463607439` passed with that compiler. Independent
checks matched the source archive, compiler record, all three drift checks,
and all 415 generated files. Only the common controller runtime, CLI compiler
identity, and three generation state files changed. This branch uses the new
API for retained account cleanup and provider inventory, with a 750 ms action
limit. The two-second work limit, one-second commit limit, page sizes, and
sweep interval remain unchanged. The restart result is recorded below; capacity
qualification is still required. No improved timing is claimed. See the
[generation record](scan-action-budget-generation-evidence.json).

Main quota/journal run `35460802209` passed all 32 required top-level checks.
Main API run `35460802217`, attempt 2, passed all 52 required checks, with no skip
or failure. Four generated hash records match, and fixture cleanup passed.
The main cluster browser attempt `35460802239`, attempt 2, stopped before Job
creation because the installed Sandbox candidate policy differs from main.
The exact policy comparison is saved. No cluster policy changed for this test.

Main workflow `35460802241` passed at `034b46b`. The core suite passed 315
top-level tests and 754 cases. Four declared live tests were excluded from that
suite. The separate API gate covers the supplied PostgreSQL workflow. Hosted
browser, console, and image jobs passed. The separate cluster browser mismatch
and deferred Kata test remain open.

The six-account regression run `35462485679` used the old compiler at
`eed9066`. Only `TestGatewayAccountCleanupBudgetResumesAfterRestart` failed.
The other 32 journal tests passed, with no skip. The failed test found that
the work deadline saved a failed cycle instead of a clean partial pass.
It uses real PostgreSQL and a provider fixture with a 450 ms action delay.
It does not measure real-provider capacity. The same test must pass after
the common scan runtime is adopted.

After adoption at `032d56b`, journal run `35463688587` passed all 33 required
top-level tests, with no failure or skip. The original six-account fixture is
unchanged. It now saves a clean partial pass, reconstructs the service and
store, closes each account once, writes all six success audits, and seals the
provider scope. This is PostgreSQL and provider-fixture evidence, not a
real-provider capacity pass. See the [journal record](scan-action-budget-journal-evidence.json).

Generated Gateway console run `35463688677` also passed at `032d56b`. Its saved
archive matches all 129 module files. The image contains the checked binary,
uses user `65532:65532`, and retains its digest after registry pull. This covers
the module, dashboard binding, private deployment, dependencies, and image.
It is not a live cluster browser result. See the
[console record](scan-action-budget-console-evidence.json).

The fifth capacity run, `35463991341`, used `032d56b` and the unchanged fixture
from the fourth run. Cleanup completed in 93.2722 seconds, so the 30-second
target failed. The scope sealed at 92.9396 seconds. All 100 selected provider
clients and users were absent. All 100 journals were closed, all 100 account
rows were closed, and all 100 success audits were present. The 9,900 background
account rows and 10,007 other provider clients matched their saved state.
Source and binary hashes matched, and test resource cleanup passed.

All recorded account scan checkpoints have clean failure flags. The first five
passes closed 18, 38, 59, 80, and 99 account rows. Their saved progress was about
12 seconds apart. There were 128 trace groups, with no dropped group. Full
trace sampling remained active. This establishes complete account cleanup and
background preservation for this fixture. It does not establish the latency
target, total Gateway cleanup, or an uninstrumented performance baseline.
The next common runtime change will permit bounded parallel actions, with
serial actions for each resource key and safe prefix checkpoints.

The full hosted application run `35463688329` passed at `032d56b`. It passed
325 top-level tests and 816 cases. Every core case from main's earlier run
remained present. The branch adds ten top-level tests and 62 cases for Sandbox
policy, bounded diagnostics, and cleanup restart. The four declared live tests
and the separate capacity test were excluded from the core suite. Hosted
browser, UI, and image jobs passed. The live Kata job was not selected.

The first result collector expected only the new restart case. Source review
confirmed the other branch additions and the capacity exclusion. The corrected
collector verified the existing result; CI was not repeated. See the
[full hosted record](scan-action-budget-full-evidence.json). These branch results
do not promote the pending Sandbox changes or close the live browser policy
mismatch on main.


Compiler `d3ccd11` passed all six branch and main compiler jobs and was published
as an authenticated immutable package. Hosted regeneration `35466521243` passed
at pin source `aa1a306`. All 416 generated files and all three drift checks
matched. See the [parallel generation record](scan-parallel-generation-evidence.json).

The application now uses STEGO's keyed parallel scan API, with eight cleanup
workers by default. Account rows and retained journals share the account ID key.
Provider inventory uses its client ID key. Worker scheduling and checkpoints
remain in generated code. The worker setting is separate from quotas and uses
the exported common ceiling. Existing serial constructors and the six-account
restart fixture remain unchanged. Work budgets, page sizes, sweep cadence, and
the capacity fixture are unchanged.

A new 32-account PostgreSQL test checks saved partial progress, independent
parallel actions, serial row/journal actions for each account, callback joins,
store reconstruction, scope closure, and one success audit for each account.
Application regression and real-provider capacity results are still pending.
The last measured complete cleanup is 93.2722 seconds; no timing improvement
is claimed for the new candidate.


The first parallel journal run, `35466619525` at `f38f334`, failed the new test's
scope revision assertion. The other 34 required tests passed, with no skip.
The original six-account fixture passed unchanged. The new test reached a
sealed scope at revision 33 but expected revision 32. The generated storage
contract increments the revision for each new key and once more for sealing.
The corrected test requires revision 32 before completion and exactly one
further revision for sealing. Its later provider absence and per-account audit
assertions were not reached in the failed run and still require a passing run.
See the [failed journal result](scan-parallel-first-journal-evidence.json).
No production code, work budget, or capacity fixture changed for this correction.


The corrected journal run `35466947578` passed at `6c54674`. All 35 required
top-level tests passed, with no failure or skip. The unchanged six-account
serial fixture passed. The new parallel fixture saved 24 closed account rows
before reconstruction and reached eight concurrent callbacks. After restart,
all 32 account rows and journals were visited, all provider clients were absent,
the scope was sealed, and each account had one success audit. See the
[parallel journal record](scan-parallel-journal-evidence.json).

This result checks PostgreSQL recovery with a provider fixture. The full hosted
application check and sixth real-provider capacity result remain pending. The
previous complete cleanup measurement remains 93.2722 seconds. The separate
[Gateway identity discovery change](gateway-identity-inventory.md) is excluded
from the frozen source for that sixth capacity comparison.
