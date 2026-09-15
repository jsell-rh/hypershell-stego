# Test conversion after database catalog removal

The `ManagedDatabase` API and its server-resource controller are retired.
Their CRUD, provider-selection, observation, and replay tests no longer describe
an application interface. Git history retains those tests and their old evidence.

The real browser workflow now passes with installation-supplied PostgreSQL.
The [recorded result](controller-local-browser-evidence.json) includes the six
failed attempts and the remaining full-package limits. This result does not
make the full application CI gate pass.

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

The third run reached SQL provisioning and started both Gateway Pods. Their
TLS library then rejected the fixture's CA certificate as a server certificate
(`CaUsedAsEndEntity`). The fixture now issues a separate server certificate
with `CA:FALSE`, server-authentication usage, and explicit DNS and IP names.
It checks the chain and names before cluster deployment. The CA signing key
is removed after certificate issuance. The clients receive only the CA
certificate, and PostgreSQL receives only its server certificate and key.

The fourth run passed real Gateway SQL isolation, verified RPC, denied calls,
and data recovery after Gateway Pod replacement. It then failed an old test
expectation that the allocator could not patch namespaces. The declared public
identity record requires that grant. The test now checks the grant and makes
two additional server dry-runs: fingerprint replacement and removal. Both
must fail under the generated ownership policy and leave the record unchanged.
The existing denial with the test actor remains. No runtime permission changed.

The fifth run passed those admission checks, all 15 worker access checks,
and SQL isolation after all three worker Pods were replaced. Gateway SQL and
credential identities remained stable. REST and gRPC access checks also passed
after API and console replacement. The rendered collector-outage step then
failed: the browser received `503` responses while one session remained in
`refreshing`. Its observed age was 20.32 seconds. The cause remains under
investigation in STEGO's common session runtime.

The [attempt record](controller-local-browser-attempts.json) retains all five
failures, generation hashes, and cleanup results. Every failed Job and its
owned resources are gone. Normal Gateway deletion and the final event and
telemetry checks were not reached in the fifth run. The complete browser gate
remains open.

The sixth run used STEGO browser backend 1.6.1. Its common session runtime
rolls back an incomplete refresh claim. The rendered renewal and collector
outage checks passed. Worker telemetry, service-account actions, and Gateway
SQL deletion also passed. The final installation-data read failed because the
test omitted the `public` schema. The generated SQL reader uses the fixed
`pg_catalog` search path. The fixture now uses `public.installation_data` and
makes the same read before it starts the workflow. No runtime SQL permission
or search path changed. The sixth Job and its resources are gone, and the
shared Lease is free. A new complete browser result remains required.

## Supplied-server browser result

The seventh run passed `TestGeneratedKubernetesBrowserGatewayWorkflow` with race
detection in 301.84 seconds. It used compiler
`421ce6b53a40850a3241cd1582843e65d6709fc9` and frozen application source
`ef538c4`. The generated API, console, provisioner, and three workers ran in a
bounded jshell namespace. The test used the actual OpenShell Gateway and
Keycloak. It did not use Playwright.

The workflow created Gateways without a database field or database catalog.
It checked owner grants, filtered access, denied calls, REST and gRPC, and
generated event delivery. Both Gateways had separate SQL databases and logins
with verified TLS. Cross-database access failed. The generated admission rules
rejected all five forbidden changes, and all 15 worker access checks passed.

Gateway and worker replacement preserved keys, SQL credentials, and stored
provider data. API and console replacement, session-key rotation, renewal,
collector loss, and provider logout passed. All three workers emitted metrics
and correlated logs and traces before and after replacement. Browser service
accounts completed creation, token handoff, use, reload, revocation, and deletion.

Normal deletion removed Gateway SQL, roles, credentials, workload and state
namespaces, and allocation bindings. The other Gateway stayed available after
the first deletion. The supplied PostgreSQL server and its installation data
remained after both deletions. Parent deletion then succeeded and delivered its
event through the generated runtime.

Both direct generation runs and the post-test check produced the same hashes.
All 227 archived output, state, dependency, and compiler-reference files match
the checkout. The Job completed with exit zero. All owned resources are absent,
and the shared Lease was released. The rendered Gateway page was inspected.

The run used the explicit 126-file transition list. Three old live test files
still prevent the full acceptance build and dependency check. The recorded
full CI run confirms that failure; its SQL adapter test passed before generation
stopped on the old import. Deletion before the first worker run, the remaining
SQL fault tests, database-server restart, installation CNPG, and actual RDS
remain required. This browser result does not establish production readiness.
