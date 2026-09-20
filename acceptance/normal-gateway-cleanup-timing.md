# Normal Gateway cleanup observations

The live browser workflow records normal final Gateway deletion separately from
its earlier SQL-denial recovery case. Each sample starts after the owner receives
HTTP 202. Before that request, a bounded SQL read records the total and undeleted
account row counts. An already-deleting Gateway cannot start a new sample.

A sample completes only after the existing checks find all four allocated
namespace profiles absent, account and identity cleanup complete, workload and
SQL cleanup finalized, allocation bindings absent, and Gateway and console SQL
roles and databases absent. The owner API must also return HTTP 404. The test
then checks that the supplied PostgreSQL server and installation data remain.

Completion checks are sequential. The elapsed value is an observed upper bound,
not an exact controller completion time. An upper bound at or below 30 seconds
proves that this sample was observed complete within the target. A larger upper
bound cannot prove when an earlier unobserved completion occurred. It requires
further measurement; it must not be reported as an exact latency failure.

The record is `gateway-cleanup-timing.json`. It includes the observation method,
phase, acceptance time, account counts, per-Gateway completion state, elapsed
upper bounds, and preservation result. Partial records survive a test failure.
A successful live workload run must supply the file; missing and empty files
fail evidence collection. The record contains no credentials or response bodies.

Schema 2 prepares 100 live accounts for each measured Gateway before any
measured deletion starts. Each account goes through the real browser backend,
REST API, and provider. The test checks token issuance and the stored ready
identity. Creation is serial, with a five-minute total context and the existing
15-second browser request limit. It makes no account write retries. The fixture
sets both account quotas to 100; production defaults and operator choices stay
unchanged.

After cleanup, every selected provider client and user must be absent. Every
protected journal must authenticate and close its exact provider identity. The
account scope must be sealed. Every account row must be closed with one success
audit. These checks run inside the existing cleanup deadline and before the
elapsed upper bound is recorded. Partial counts survive failures.

This remains a correctness workflow with two Gateways and the ordinary test
identity provider. It does not run 100 Gateway workloads, seed 9,900 background
accounts, or use the separate production-mode provider capacity fixture. Its
record therefore keeps `capacity_fixture` false. A passing timing observation
at this population does not qualify the whole production capacity target.
The 30-second target and the existing fault and late-effect checks are unchanged.
Hosted adapter run `35476824851` compiled source `f924e96`, including the
100-account population. All 63 required adapter tests, four constructor cases,
six allocation cleanup tests, and 28 collection cases passed. The source archive
matches the committed candidate. The adapter-only SQL exclusion is unchanged.
See the [compile evidence](normal-cleanup-population-compile-evidence.json).
Live execution remains required. Earlier results below apply to earlier source.

The local collection test uses fake files and a fake `oc` command. It passed with
the new missing-file and empty-file cases. Hosted adapter run `35475728036`
passed all 63 required tests and all 28 collection cases. The live test compiled.
The SQL integration test remains excluded from this adapter-only gate. See the
[adapter evidence](normal-gateway-cleanup-timing-evidence.json).
The live observation remains pending. No live timing result is claimed.

Live run `35476176959` stopped during installation preflight, before it created
the test workload. The rendered inspection roles require three named reads that
the saved installation does not contain. The comparison rejected that mismatch.
An independent check found no test resources, a free lease, and all 32 standing
resources unchanged. See the [preflight evidence](normal-cleanup-preflight-evidence.json).

The browser workflow now permits a complete operator-held lease pair for normal
inspection as well as native packet tests. It rejects partial pairs. Native
packet tests still require the pair. The existing server-side lease verification
checks its exact UID, holder, namespace, and Job before work. Cleanup retains an
operator-held lease until the operator restores the temporary installation.
All eight input combinations passed a local shell check. No cluster policy was
changed by that check. The next live run requires a verified policy render and
an operator journal for the two inspection-role changes.

Live run `35477799297` at `f924e96` failed when the scheduler preempted the
test Pod for an OpenShift image registry Pod. The registry's events report
insufficient CPU and memory. The test produced no completion record, complete
evidence archive, or 100-account cleanup result. Its final container log was
unavailable after termination. The saved partial log contains ten setup test
passes and the start of the browser workflow. These observations do not qualify
the complete workflow or establish an application assertion failure.

The compiler transfer and Job limits were independently verified before the
interruption. The temporary installation was restored. Independent cleanup
found no test resources, all 32 standing resources intact, and a free lease.
See the [failed-run evidence](normal-cleanup-preemption-evidence.json).
The next attempt requires a capacity check and better terminal Pod evidence.
The default branch remains unchanged.

Candidate `4c6744b` saves bounded terminal Pod status before it collects logs.
A missing completion record still fails. Hosted run `35478679579` passed five
status cases, eight completion cases, 63 required adapter tests, six allocation
cleanup tests, and 28 collection cases. The live test compiled. See
`terminal-pod-evidence.json`. Live run `35478851396` uses this exact source.
Its result remains pending. The application and 100-account test are unchanged.

## Complete live result

Run [35478851396](https://github.com/jsell-rh/hypershell-stego/actions/runs/35478851396)
passed at `4c6744b`. All 11 required live tests passed. The browser workflow took
737.07 seconds. Independent checks matched 1,544 source files, 421 generated
file hashes before and after the test, and the signed compiler package. Six
browser runtime instances supplied all required startup logs, spans, metrics,
and active records. All four saved views were inspected; no layout defect was
seen at the tested viewport. See `normal-cleanup-live-evidence.json`.

One remaining Gateway had 100 accounts created through REST, with verified
token issuance. Deletion closed all 100 rows and authenticated journals,
removed all 100 provider clients and users, sealed the account scope, and
retained one successful cleanup audit per account. The complete observation
includes namespace, SQL database, role, binding, key, and final Gateway checks.
The supplied PostgreSQL server and installation data remained.

The observed cleanup upper bound was **53.547650006 seconds**. This does not
prove the 30-second target. The checks run in sequence; this record does not
identify the time when each resource was removed or the cause of the delay.
The result is a functional pass, not production capacity qualification.

The independent permission review checked 24 ready-Pod observations across
two Gateways and three namespace incarnations. Worker writes to service
accounts remained denied. See `normal-cleanup-account-evidence.json`.
Kata and native Sandbox packet probes were not run.

Independent cleanup found no test workloads, fixtures, allocated namespaces,
or held lease. All 32 standing resources were restored before CI policy
adoption. The earlier preempted result remains recorded as a failure.
