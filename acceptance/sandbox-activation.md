# Configured Sandbox allocation

The configured Gateway worker passed the complete live workflow at `7f81556`.
Run `35470884946` passed all 11 required tests and the checks described below.
The first failed run and the corrected test permissions remain recorded.
Live Kata and OpenShell Sandbox execution are still deferred.

The user selected the unchanged upstream Agent Sandbox controller as trusted
cluster infrastructure. The operator owns its installation and permissions.
The separate allocation and native packet checks passed earlier. The remaining
unconditional constructor rejection no longer describes that implementation.

The candidate removes that rejection. A nonempty Sandbox runtime setting can
construct the workload adapter with the generated allocator. Each reconciliation
still requires the assigned Sandbox namespace and account, verifies admission
with positive and negative server dry runs, and verifies the client certificate
before it copies the Gateway client identity. A failure prevents publication of
the Sandbox configuration. Application workers cannot install the upstream
controller, change cluster roles, or change admission policy.

The production declaration still requires its selected Kata runtime. The
constructor does not establish runtime isolation. An empty runtime setting keeps
the existing Gateway-only behavior. The operator must install the declared
allocation policy and selected runtime before using the Sandbox option.
The new constructor test checks configured startup and rejects an invalid
runtime, missing control namespace, or missing SQL state without Kubernetes
requests.

The native network fixture now also selects its test runtime for the actual
Gateway workload worker. It must pass the real namespace, account, admission,
and identity-copy path. Before and after recovery, the fixture checks the
published namespace, account, runtime, and sidecar settings, plus the exact
three copied client identity fields. It does not save credential bytes. This
extends the earlier allocation and packet checks; it starts no OpenShell
Sandbox workload. At `674c6e5`, the native test declaration changed only the runtime
class guard. The corrected test inspection permissions are described below.

Hosted adapter run `35467508365` passed at `674c6e5`. All 63 top-level tests
passed, including the four new constructor cases. One declared SQL test was
skipped because this job has no database fixture. The deferred acceptance test
compiled, and all six allocation cleanup tests passed. The exact source archive
matched. See the [adapter record](sandbox-activation-adapter-evidence.json).

Full application run `35467762040` passed at `674c6e5`. It passed 329 top-level
core tests and 844 cases, including all 816 prior cases and 28 added cases.
Both cleanup restart tests, identity discovery, and configured Sandbox startup
passed. Browser, UI, and image jobs passed. The same five conditional tests were
excluded, and the Kata job was skipped. See the
[full application result](sandbox-activation-full-evidence.json).

The bounded native cluster workflow has qualified this candidate, as recorded
below. Live OpenShell Sandbox execution and Kata isolation remain unverified. The user deferred the Kata test because no suitable cluster exists.
The upstream workspace helper and socket settings remain unchanged. No fork or
new admission service is required.


Policy render run `35467767433` passed at `674c6e5`. Independent verification
matched all 1,494 source hashes, the fixture changes, and the published compiler
records. Of 32 installation resources, only the Sandbox Pod policy's runtime
class comparison differs from the saved production installation. Permissions,
network policies, and the other 21 Pod validation rules remain unchanged.
This permits review of the native test plan; it does not apply it or prove live
behavior. See the [policy record](sandbox-activation-policy-evidence.json).


The first activation workflow, run `35469445026` at `674c6e5`, failed after
251.50 seconds. The new test reader received HTTP 403 when it read
`openshell-gateway-config`. Its inspection role did not permit that named read.
The later identity comparison also needs named reads of `openshell-client-tls`
in the Gateway and Sandbox namespaces. The test did not reach the native packet
checks or regeneration after the workflow. See the
[failed result and cleanup record](sandbox-activation-first-live-evidence.json).

The operator restored the production installation after this failure. An
independent check verified all 32 installed resources, no test resources, no
temporary RuntimeClass, and a free test lease.

Candidate `7f81556` adds only these named `get` grants to the test inspection
roles. It does not grant Secret list or write access. The application workers'
permissions remain unchanged. Eighteen inspection checks reject broader grants
and changes to existing bindings. With the installation checks, all 35 local
Python tests passed. Hosted adapter and rendered policy checks passed before
the corrected live workflow, as recorded below.

The production source, generated output, compiler pin, and Go tests match
`674c6e5` exactly. The prior full suite therefore covers those unchanged files.
The complete suite was not run again at `7f81556`; only the two test Python files
and documentation or result records differ. See the
[source comparison](sandbox-inspection-source-evidence.json). The live result below verifies the corrected inspection path.


At `7f81556`, adapter run `35470497014` passed all 63 top-level tests, including
the four constructor cases. Its declared SQL test was skipped without its
fixture. The deferred live test compiled and all six cleanup helpers passed.
See the [corrected adapter result](sandbox-inspection-adapter-evidence.json).

Policy run `35470507864` passed at the same source. Independent verification
matched all 1,502 source hashes and the signed compiler records. Of the 32
installation resources, only the two fixed test inspection roles and the
Sandbox runtime class comparison differ. The other Pod guards, network
policies, and production permissions remain equal. See the
[corrected policy result](sandbox-inspection-policy-evidence.json).

The reviewed test fixture was installed under the shared lease for live run
`35470884946`. Its completion and cleanup are verified below.


The corrected workflow passed in 840.10 seconds. Independent review matched
all 1,502 source files, the signed compiler package, and 417 generation hashes
before and after the test. It verified REST and gRPC access, filtered lists,
denied writes, event delivery, account cleanup, browser sessions, database and
provisioner restart, namespace replacement, and correlated logs, metrics, and
traces. Four captured UI images were reviewed. See the
[complete live evidence](sandbox-activation-live-evidence.json).

The actual Gateway worker's Sandbox setup passed before and after recovery.
All 34 permission checks, four denied Sandbox writes, six denied admission
requests, and 24 Gateway network connections passed. The native Sandbox probes
passed 40 paths: eight allowed connections and 32 denied connections. Four
listener Pod identities matched their allocated namespace and account records.
The account review checked 24 readiness observations across three namespace
incarnations and six account objects.

The operator restored all three changed test resources and removed the temporary
RuntimeClass. Independent cleanup verified all 32 installed resources, no test
resources or allocations, and a free shared lease. The result does not prove
VM isolation, actual OpenShell Sandbox execution, RDS failover, or production
capacity. The selected upstream controller trust boundary remains unchanged.


## Main application check

Run `35472230170` passed at main source `dbd8363`. It passed 329 top-level
core tests and 844 cases, with no failed cases. It retained the five conditional
exclusions in the [complete result](sandbox-activation-main-full-evidence.json).
Serial and parallel restart tests, the rendered browser workflow, the UI, and
the service image checks passed. The deferred Kata job was skipped.

The production source matches the earlier qualified workflow. The two changed
mechanism files add and test named read permissions for the live test inspector.
Two saved browser views were inspected. The provisioning view has a loading
placeholder for connection commands. The account view shows an empty list after
deletion. These views do not establish live Gateway readiness or Sandbox
execution. Existing live workflow evidence and its limits remain separate.
