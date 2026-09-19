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

STEGO candidate `58a3bcc` adds an action time reserve to the common scan API.
It stops a pass before a new action if a full action budget is not available.
Real errors remain in the saved cycle. This candidate is in compiler CI;
Hypershell has not adopted it, and no improved timing is claimed.

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
