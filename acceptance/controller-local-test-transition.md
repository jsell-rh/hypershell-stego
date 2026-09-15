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

## Real SQL adapter check

`TestGatewaySQLUsesDurableStateAndRetainsSuppliedServer` passed with race
detection in a bounded jshell Job. It uses a real PostgreSQL server and a
non-superuser provisioning account. Two Gateways have separate databases and
logins. Each login can write its own data. Connections to the other Gateway,
the provisioning database, and `postgres` fail with permission errors.

The check creates a new adapter and verifies the original keys, credentials,
and data. A changed SQL destination or missing source Secret stops creation
and cleanup. Cleanup waits for workload namespace removal, removes only the
target database and roles, and rejects a late creation retry. The other
Gateway and the installation data remain usable. Repeated deletion succeeds.

The test took 0.900 seconds. Its package took 1.913 seconds after compilation.
The Job completed with exit zero. Its Pods and private fixtures are absent,
and the shared Lease was released. The [evidence](controller-local-sql-evidence.json)
records the source hashes, image pins, limits, and cleanup results.

Kubernetes records use an HTTPS fixture in this check. Restart means a new
adapter; this check does not restart the API process or database server. It
does not prove real Gateway Pod, browser, CNPG, or RDS operation. Those checks
and the complete acceptance build remain open. The existing API process
restart evidence remains a separate result.

The jshell API gate now requires this real SQL check before generation and
the application tests. It still requires the full acceptance package. A pass
in the SQL check cannot hide a later build or application test failure.

## Browser fixture conversion

The browser fixture now supplies PostgreSQL before it starts the generated
workers. Its installation account creates a separate, non-superuser SQL
provisioning account. The Gateway worker receives that account through its
declared file Secret. The fixture checks the existing component database names
before it removes default PUBLIC connection access. It refuses a foreign
database. Gateway reconciliation does not change that installation policy.

The workflow has three workers: namespace allocation, Gateway identity, and
Gateway workload. Gateway deletion must remove its SQL database, both SQL
roles, workload namespace, and retained state namespace. It must preserve the
other Gateway and the installation data. Parent cluster deletion must wait for
both cleanup owners. The fixture no longer starts a server-resource controller
or expects application deletion to remove CNPG storage.

The removed `TestDatabaseWorkloadAndOfflineDeletion` and
`TestCNPGDatabaseWorkloadAndOfflineDeletion` tested the retired server controller.
Their requirement to create, repair, and delete PostgreSQL server resources
conflicts with the current installation contract. Their old source remains in
Git history. CNPG as an installation-supplied server remains required.

`controller-local-browser.files` lists the converted test files for bounded
transition checks. It includes the race-enabled fixture and excludes its
non-race alternative. Three live files still require conversion:

| File | Remaining work |
| --- | --- |
| `gateway_workload_test.go` | Move the remaining fault and recovery checks from the old kind and server-controller fixture into the supplied-server workflow. |
| `cnpg_gateway_test.go` | Convert Gateway SQL fault checks that use the retired CNPG resource names and server controller. |
| `gateway_recovery_test.go` | Handle deletion before the first worker run. The current controller can wait for durable state that was never created. |

The complete generation dependency check and acceptance package remain required.
The file list does not replace them. Fixture conversion alone is not evidence
that the browser workflow passes.

The [browser preflight evidence](controller-local-browser-preflight-evidence.json)
records 13 passing API checks and two equal direct-generation hash records.
The API test package took 81.400 seconds. The subsequent browser test failed
because the runtime SQL role lacked read access to `stego_schema.generation`.
The fixture now grants schema usage and SELECT only. It checks that schema and
record writes remain denied.

The next run started the API, console, provisioner, and three workers. Both
Gateway allocations succeeded, but Gateway startup failed. The SQL egress rule
included the Service address without the backing fixture Pod address. The
fixture now supplies both observed addresses, on port 5432 only. Kubernetes
can process address translation before or after the network rule; see the
[NetworkPolicy contract](https://kubernetes.io/docs/concepts/services-networking/network-policies/#behavior-of-to-and-from-selectors).
Both failed Jobs and their owned resources were removed. Neither failure is a
complete browser workflow pass.
