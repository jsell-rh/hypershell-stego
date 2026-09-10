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
