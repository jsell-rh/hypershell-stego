# Configured Sandbox allocation

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
Sandbox workload. The native test declaration still changes only the runtime
class guard. It grants no extra permission.

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

The bounded native cluster workflow must still qualify this candidate before
promotion. Live OpenShell Sandbox execution and Kata isolation remain
unverified. The user deferred the Kata test because no suitable cluster exists.
The upstream workspace helper and socket settings remain unchanged. No fork or
new admission service is required.


Policy render run `35467767433` passed at `674c6e5`. Independent verification
matched all 1,494 source hashes, the fixture changes, and the published compiler
records. Of 32 installation resources, only the Sandbox Pod policy's runtime
class comparison differs from the saved production installation. Permissions,
network policies, and the other 21 Pod validation rules remain unchanged.
This permits review of the native test plan; it does not apply it or prove live
behavior. See the [policy record](sandbox-activation-policy-evidence.json).
