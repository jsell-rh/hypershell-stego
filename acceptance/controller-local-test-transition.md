# Test conversion after database catalog removal

The `ManagedDatabase` API and its server-resource controller are retired.
Their CRUD, provider-selection, observation, and replay tests no longer describe
an application interface. Git history retains those tests and their old evidence.

The conversion keeps tests for behavior that remains required:

| Retired test area | Current check or remaining work |
| --- | --- |
| Local catalog selection and deployment-backed database placement | `controller_local_schema_test.go` and `gateway_request_contract_test.go` check creation without a catalog and rejection of retired input and old schemas. Deployment-backed servers remain unsupported. |
| Database controller write grants and database resource revisions | `controller_local_cleanup_test.go` checks exact SQL cleanup grants, versions, and restart. Gateway workload observations retain their existing tests. |
| Database retained reads and replay | Gateway recovery IDs and cursor tests check retained discovery. Controller scheduling tests check restart, independent progress, and cluster isolation. The database replay RPC no longer exists. |
| Database cleanup deadlines | `gateway_observation_deadline_test.go` checks pending SQL cleanup through a deadline and API restart. Workload and identity tests still check late effects after a prior completion. |
| Cluster and release APIs, CLI, and apply | The converted tests retain transport contracts, access, events, rollback, repeated apply, and restart. Parent deletion checks both Gateway cleanup owners. |
| Old placement-catalog migration | The retired catalog has no migration into this generation. Fresh-schema and legacy-schema rejection tests replace that path. The historical migration file remains unchanged. |
| SQL server and Gateway workload lifecycle | Live fixtures still need conversion to installation-supplied servers. This work is incomplete. |

SQL cleanup completion is durable. The SQL provisioner keeps a deletion record
that prevents later creation. Workload cleanup continues to check for late
Kubernetes effects. The tests must preserve this distinction; they must not
require a removed credential Secret to repeat completed SQL deletion.

The full acceptance package is not yet restored. The remaining mixed catalog,
live Kubernetes and browser fixtures must be converted before the full
application gate can pass. Selected test results do not replace that gate.

The expanded check has passing results for all 81 selected application tests and
119 top-level tests in total. The full run passed 78 application tests. A bounded
repeat passed the three corrected tests and the related cursor check. The
corrections use the database's ID sort order, remove an obsolete CLI table count,
and give the two cleanup phases a 180-second bound at the same one-CPU limit.

The backlog check kept 1,280 retained Gateways across a 1,024-key queue. All
1,279 healthy Gateways completed SQL and workload cleanup in 148.028 seconds.
The failed provider stayed pending. The test checks phase order and both final
cleanup records. This is a bounded correctness check, not a production rate.
The earlier 90-second failure remains in the evidence.

The production build and two direct generation runs passed. Generated files
match the working tree. The full generation dependency check remains open
because older live fixtures still import removed packages. These selected
results do not replace the complete acceptance build or the real Gateway Pod
and SQL lifecycle gate.

[Recorded results](controller-local-extended-evidence.json) include both runs,
the earlier compile failures, test names, frozen source hashes, generated file
hashes, limits, and resource removal. All test Jobs, Pods, and private fixtures
are absent. The shared test Lease was released.

`controller-local-extended.files` contains the explicit test file list. Run it
only in CI or a bounded cluster Job, with PostgreSQL required. From `acceptance`:

```sh
xargs go test -race -mod=readonly -count=1 -timeout=12m < controller-local-extended.files
```
