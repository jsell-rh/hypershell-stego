# Common database credential preparation

Gateway and console setup use STEGO's `PrepareDatabaseCredentials` operation.
The common component checks the server identity, stored SQL ownership record,
and SQL names under one resource lock before it returns candidate credentials.
Hypershell selects the resource key and secret fields. STEGO's existing secret
state operation retains the selected candidate before database creation.

The SQL workflow also requires a deleted Gateway's retained SQL record to reject
new application credentials. Existing checks cover separate logical databases,
limited logins, cross-database denial, stored keys, restart, and cleanup.

This branch selects compiler and common registry
`b8fdfd6946a740430a2a4f41cf9aaae03a6d6793`, including `postgres-client` 1.5.0.
The common compiler, SQL lifecycle, provider, storage, and example checks passed
in run `35217276981`. The signed main package from run `35218936816` was checked
against the exact source and two independent builds. Its immutable release and
common installer were also checked. The compiler SHA-256 is
`2913c048a2ddd64ca55af9ad4af485167ea6c07718fa55c577b475cb55608160`.

CI regeneration passed in run `35219768157`. Independent checks matched all
415 archived files, the exact source, the installed release record, and all
three drift checks. The new credentials file was imported from the verified
archive; the CI patch omitted it because it was untracked. See the
[generation record](database-credential-generation-evidence.json).

The dedicated API and SQL checks passed, including the new deleted-record
assertion. The first complete live Gateway workflow with this compiler failed at the
PostgreSQL telemetry check. The corrected complete workflow later passed at
`d9cd7e3`; see [the workflow record](postgres-signal-cnpg.md). Later source
changes retain their own qualification requirements.

## Verified hosted results

At application source `c55b2e2`, provider run `35219968953` passed in
123.06 seconds. Independent checks confirmed all 61 protected closure records,
20 journals at process restart, denied account creation, preservation of the
unrelated client, generation, and cleanup. All 15 comparison cases and the
cleanup deadline case also passed. This repeat had equal raw client response
bytes. It does not explain the earlier scope-array difference or event timeout.
See the [provider record](database-credential-provider-evidence.json).

The rendered management console passed in 78.12 seconds in browser job
`105197440700` of run `35219979081`. Login, Gateway creation, grants, REST and
gRPC access, events, restart, session renewal, key rotation, and logout passed.
All eight startup stages had correlated logs, traces, and metrics for three
process instances. Both screenshots were reviewed. They show a Gateway still
in provisioning and the empty account list after deletion. This hosted fixture
does not run the real Gateway. See the
[browser record](database-credential-browser-evidence.json).

The Gateway console module passed run `35219945091`. Independent checks matched
129 source files, repeat generation, dependency checks, the image executable,
and the verified compiler installer. The executable changed with the new common
tracing operation. See the [module record](database-credential-module-evidence.json).
Regeneration run `35220022601` matched all 415 files, produced an empty review
patch, and passed all three drift checks. See the
[repeat generation record](database-credential-regeneration-evidence.json).

The core suite passed at the same application source in job `105197440867`
of run `35219979081`. Independent log checks confirmed 309 top-level tests and
668 test pass events, with no failures. Both earlier failure cases and all
15 scope comparison cases passed. Three generation drift checks and hosted
service cleanup also passed. The four excluded live tests have separate gates.
See the [core record](database-credential-core-evidence.json). This pass does
not establish the cause of either earlier failure.

The later complete workflow passed all 11 required tests, including credential
preparation telemetry. That result does not change the earlier failed run.
The API and journal results below have separate scopes.

## API, SQL, and journal results

At source `c55b2e2`, the dedicated jshell API run `35219966768` passed all 52
required tests. Independent checks matched 1,421 source files, 416 generated
hashes, the live compiler bytes, and the published signature records. The SQL
workflow exercised common credential preparation and rejected new credentials
for a deleted Gateway's retained SQL record. Creation, access rules, events,
delete, restart, and regeneration also passed. Independent cleanup confirmed
that test resources were absent and the shared test Lease was empty.
See the [API record](database-credential-api-evidence.json).

Journal run `35219971392` passed all 28 required tests exactly once, with no
failed or skipped test. Generation and hosted service cleanup passed. The tests
cover protected journals, access rules, provider failure, concurrent registration,
event rollback, and checkpoint recovery. They do not prove whole-database
restore or distributed writer fencing. See the
[journal record](database-credential-journal-evidence.json).

## Failed complete CNPG workflow

Run `35220931665` at source `fd8285d` failed after 751.33 seconds in
`TestGeneratedKubernetesBrowserGatewayWorkflow`. Ten preceding tests passed.
Independent checks matched 1,428 source files, all 416 generated hashes before
the tests, and the live compiler package. The test reached Gateway deletion
and database retention checks before it rejected PostgreSQL telemetry.
Generation after the tests was not reached. See the
[failure record](database-credential-cnpg-failure-evidence.json).

The generated runtime emits the common `prepare` database operation. The
application telemetry test did not permit that operation. The corrected test
permits `prepare` and requires its correlated logs, traces, and metrics. It
still rejects unknown operations. The failed run retained an invalid flag,
so this contract defect does not exclude another invalid field or identity.
The complete workflow passed again with the corrected contract in run
`35224348179`. Its exact source and limits are in the
[complete workflow record](postgres-signal-cnpg.md).

Independent cleanup confirmed that the test resources, both volumes, and
the shared test Lease holder were absent. The previous failure remains in
the evidence record.

The focused contract check passed in
[run 35223819383](https://github.com/jsell-rh/hypershell-stego/actions/runs/35223819383)
at source `e98b01f`. Independent checks confirmed all three PostgreSQL signal
tests, including credential preparation with a controller parent, log, trace,
and metric. The provider workflow closed 61 protected identities and retained
20 journals across restart. All 15 comparison cases, the deadline case,
generation, and cleanup passed. See the
[contract check record](postgres-signal-contract-evidence.json). This focused
pass does not replace the complete CNPG workflow.
