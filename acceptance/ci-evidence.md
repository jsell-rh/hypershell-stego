The full application gate must produce a result even while work continues.
On 2026-09-10, frequent pushes canceled the seven most recent completed workflow
runs. Run 34496871848, for commit
`3dbbac9b83b8ad9c0382273f0100c9be6e8eac5f`, passed the database, Gateway workload,
and sandbox workload jobs. Its complete acceptance job was canceled while
`scripts/check-gateway.sh` was running. It did not produce a full suite result.

The workflow now allows an active push run to finish. A newer push can replace
one pending run. Pull requests still cancel obsolete runs for the same ref.
This preserves complete results for started push runs without accumulating an
unbounded queue. It does not guarantee a run for each intermediate commit.
See GitHub's [concurrency contract](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#concurrency).

At that revision, all four jobs and their time limits remained in force. The acceptance
job requires PostgreSQL and Keycloak, verifies modules and pinned regeneration,
and runs the complete Go race suite. Separate jobs check real database, Gateway,
and sandbox workloads. Report each result with its tested revision and scope.
Cancellation supplies no evidence that an unfinished test passed or failed.

YAML parsing and a structural comparison confirmed that only the cancellation
expression changed. No application or generated source changed. This workflow
change does not require a compiler pin update.

On 2026-09-10, run 34523173525 for commit
`a844cd6de6eaa51da1b7a15f901db875cb108f05` reached the acceptance package's
18-minute limit. Its five separate provider jobs passed. The package timeout
occurred during `TestGeneratedServiceAccountCLIWorkflow`, after that test had
run for 35 seconds. The log reported no assertion failure before the timeout.
The incomplete suite is a failed CI result.

The same CLI test passed locally at commit
`132177d82b0145803c59e03502294c77fbee22f8` in 31.02 seconds under race detection,
with PostgreSQL and Keycloak required. This result does not replace the failed
full CI run or prove its cause. The previous local full acceptance run took
869.854 seconds, before the latest inventory restart test was added.

The aggregate package limit is now 25 minutes. The CI job allows 30 minutes for
setup, module checks, regeneration, and tests. Request, provider, and individual
workflow deadlines are unchanged. Verbose test output records each test's
progress and duration. A future timeout can then be assessed from individual
test results. The full remote suite must still pass at the new revision.

Run 34525332413 for `7dfc96a3c1d189a48ca40b4243d1d81279eade29` also reached
the old 18-minute package limit. `TestServiceAccountCreationFailuresAndRevokedAccess`
had been active for two seconds. The failure log reported no earlier assertion
failure. This run still used the old limit. The later health run for `d162879`
uses the 25-minute package budget and must produce its own complete result.

The [health revision run 34527122554](https://github.com/jsell-rh/hypershell-stego/actions/runs/34527122554)
completed successfully for `d162879d1ef73d5121c9b23e7931f63a5ff17b77`.
All six jobs passed, including the complete acceptance suite with the new
25-minute package limit and the five provider jobs. This supplies a complete
result for that revision. It does not replace CI for the later tracing or
request-observability changes.

The [compiler-validation revision run 34529576536](https://github.com/jsell-rh/hypershell-stego/actions/runs/34529576536)
completed successfully for `b157522f526c0a01b036f7bc0e0cb614ba8db5ea`.
All six jobs passed, including full acceptance and the five provider workflows.
This result precedes request logs, service logs, and controller telemetry. Those
later revisions require their own CI results.

Run [34533977192](https://github.com/jsell-rh/hypershell-stego/actions/runs/34533977192)
for `d5329dc` exposed old log checks after controller telemetry moved into STEGO.
The database and CNPG database tests waited for provider error text. The Gateway
and Sandbox tests waited for gRPC error text during database cleanup. Those four
provider jobs failed. The CNPG Gateway job passed. The acceptance job was still
active when this record was written. The failed jobs are not successful evidence.

The tests now read the common structured retry event. Database tests also call
the provider and require a typed HTTP 403 result. They check that cleanup remains
pending before they restore delete permission. The Gateway test calls the
generated gRPC API and requires `Internal` while a database constraint prevents
deletion. It verifies that the database remains live before removing the fault.
Provider error text is not required in process logs.

All five provider scripts passed locally on 2026-09-10 under race detection:

| Script argument | Compiler | Acceptance package time |
| --- | --- | --- |
| `database` | `777d591` | 99.754 seconds |
| `cnpg` | `777d591` | 85.124 seconds |
| `gateway` | `6277272` | 224.241 seconds |
| `sandbox` | `6277272` | 354.428 seconds |
| `cnpg-gateway` | `6277272` | 342.162 seconds |

The scripts used real Kubernetes providers. Gateway variants also used the real
identity service and Gateway. The Sandbox gate executed its workload in the
virtual machine. These local results do not replace CI for the new commit.

Run 34533977192 has now finished with a failed acceptance result as well. The
controller telemetry test received HTTP 409 during initial Gateway creation.
The old assertion recorded no response reason, so that log does not prove which
conflict caused the response. The test started a controller and a new API user
at the same time. The API permits serialization conflicts during concurrent
transactions.

The telemetry test now retries only HTTP 409 with the documented serialization
reason. It rejects other conflicts. Five attempts bound the retry loop. A test
trigger raises SQLSTATE `40001` on its first Gateway insert. A sequence preserves
the attempt count across rollback. The test requires the failed attempt to leave
no Gateway or owner grant, then requires successful creation and reconciliation.
Thus the retry branch is exercised on every run. Three consecutive race runs
passed with PostgreSQL required, in 23.220 seconds. This does not change the API's
transaction policy or add automatic retries to application writes.

Run [34535509044](https://github.com/jsell-rh/hypershell-stego/actions/runs/34535509044)
for `ce1af8e` has finished. All five provider jobs passed. The full acceptance
suite finished in 1152.715 seconds with one failed test:
`TestGeneratedRuntimeRejectsUnknownDatabaseProvider`. The process exited with
code 1 and reported `component[4].constructor[0]`. The assertion still required
raw constructor error text, which the process privacy policy now excludes.
This was an assertion failure, not a package timeout.

The test now requires exit code 1, one safe failure record from the generated
application constructor, and no HTTP or gRPC listener startup. It rejects the
private provider setting and raw constructor error message in output. The local
race check passed with PostgreSQL required. This test change does not relax
provider validation or change the process logging policy. Later revisions still
need their own full CI result.

Run [34537318120](https://github.com/jsell-rh/hypershell-stego/actions/runs/34537318120)
for `50ae860` finished with all five provider jobs passing. Full acceptance took
1224.526 seconds and reported one failure in the same old startup-log assertion.
The process exited with code 1 and the safe constructor-stage record. This
revision preceded the assertion fix in `8064265`. The run remains a failed CI
result. Run 34538907855 includes the fix and was active when this record was
written.

Run [34538907855](https://github.com/jsell-rh/hypershell-stego/actions/runs/34538907855)
for `fdc63e50f28b71cae19e8eccd454b849c88840f2` has now passed all six jobs.
This includes full acceptance with the corrected startup-log assertion and
the five provider workflows.

Run [34539830468](https://github.com/jsell-rh/hypershell-stego/actions/runs/34539830468)
for `473b65c057561ae282930dfaef29642bea126b42` also passed all six jobs.
It includes the shared HTTP client telemetry and real Keycloak restart test.
These results precede CLI runtime telemetry. That change requires its own
full CI result.

Run [34556079041](https://github.com/jsell-rh/hypershell-stego/actions/runs/34556079041)
for `8d810f1b1d0ef647c847455f08ee771cc7ad6cde` finished with all five provider
jobs passing. Full acceptance took 1199.434 seconds and failed only
`TestGatewayNetworkWorkflowThroughGeneratedRuntime`, at its CLI create check.
The network helper combined stdout and stderr before JSON decoding. Generated
CLI telemetry now writes completion records to stderr. The helper must read
command JSON from stdout and check diagnostic records separately.

The corrected helper also checks both streams for the caller token and requires
one command completion record on stderr. Other generated CLI helpers already
separate the streams. The remaining reference CLI helper tests a separate,
optional executable. The failed full CI result remains part of the evidence;
the corrected revision requires a new full run.

The unchanged network test reproduced the same failure in the `jshell` cluster.
With the corrected helper, three consecutive race runs passed in 26.459 seconds.
Each run covered network CRUD, access, events, rollback, CLI calls, watch,
restart, and schema upgrade. The generated CLI telemetry, version, and Gateway
workflow tests also passed. Job `network-cli` used Go 1.26.8, PostgreSQL 18.6,
one test CPU, a 3 GiB test memory limit, and a 30-minute deadline. Its source
archive SHA-256 was
`1d52189b924fe1137a07d8b3ad5fbbf131f3ea1b14fdc5bca0ef3c01f389d35d`.
These focused results do not replace the required full CI result.

Run [34601323734](https://github.com/jsell-rh/hypershell-stego/actions/runs/34601323734)
for `41adffb8fece0537d0108639a11f96bbc298e34b` passed all six jobs, including
full acceptance and the five provider workflows. This verifies the CLI network
test correction. It precedes the database telemetry change.
