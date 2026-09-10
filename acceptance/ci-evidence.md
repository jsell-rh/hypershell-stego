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

All four jobs and their existing time limits remain in force. The acceptance
job requires PostgreSQL and Keycloak, verifies modules and pinned regeneration,
and runs the complete Go race suite. Separate jobs check real database, Gateway,
and sandbox workloads. Report each result with its tested revision and scope.
Cancellation supplies no evidence that an unfinished test passed or failed.

YAML parsing and a structural comparison confirmed that only the cancellation
expression changed. No application or generated source changed. This workflow
change does not require a compiler pin update.
