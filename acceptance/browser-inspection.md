The browser workload fixture now uses namespace inspection roles declared through
STEGO. Its test identity has no global Secret, Pod, Deployment, Sandbox, node, or
token-review permissions. Global rights are limited to public Namespace and RBAC
reads. Control-namespace rights still permit fixture installation and worker
token requests. The fixture is trusted test code; it can start its own workers.

The production declaration does not contain the inspection roles. Run
`scripts/prepare-browser-inspection.py --compiler <pinned-stego-binary>
--destination <new-directory>` before the workload check. The compiler binary
must have clean Git build metadata at `.stego/compiler-revision`. Preparation
copies regular source files to a new directory outside the repository. It adds
three namespace roles and one binding at the end of each corresponding profile,
then runs bounded STEGO generation and drift checks. The source repository stays
unchanged. The generated inspection images are test artifacts, not release images.

The preparation check requires these four changed files:

- `service.yaml`: the fixed inspection roles and bindings.
- `.stego/state.yaml`: the resulting compiler state.
- `out/deploy/allocation/allocation.go`: the embedded allocation configuration.
- `out/deploy/render/worker-namespace-allocation.json.tmpl`: matching roles and
  admission rules.

The check removes the three inspection roles and bindings from the generated
configuration and compares it with production. Production roles, quotas,
identities, binding indices, and executable allocator code must match exactly.
The generated inspection permissions must also match a fixed allowlist. A
bounded build of the standard-library renderer checks that only the three roles,
allocator bind names, and namespace binding cases change in the manifest. A file
outside this list that changes, appears, or disappears stops preparation. The
compiler's local lock file is excluded. `acceptance/browser-inspection-source.json`
records both complete source hash sets and the permitted differences.

In a Gateway namespace, the test identity can read five named Secrets: the
Gateway database credential, Gateway keys, public and internal server TLS, and
the console runtime files. It can also read its quota and network policies,
observe the Gateway Deployment, and create or remove bounded probe Pods. In a
Gateway state namespace, it can read the named state Secret and allocation
policy. In a console state namespace, it can only read `gateway-console-state`.
These reads compare retained credentials and session keys with the running
application. Private values remain in memory and do not enter result records.

The test identity cannot list or change Secrets. The generated allocator owns
all inspection bindings. Its admission policy prevents the test identity from
adding or changing them. Production permission checks still use the three
actual worker identities.

The six admission probes now use the allocator identity. Namespace writes by
the test identity must fail at authorization. Fingerprint changes must still
fail at the generated ownership policy. A change to the inspection binding
that selects a foreign subject must fail at the allocation policy. The live access checks include both
SelfSubjectAccessReview calls and actual denied Secret reads; a missing Secret
is not evidence of access denial.

Run the existing `scripts/check-service-deployment.sh` from the prepared directory
with `STEGO_TEST_BROWSER_DEPLOYMENT=1`, `STEGO_TEST_BROWSER_WORKLOAD=1`, the explicit
saved `STEGO_TEST_CONTEXT`, and the existing test ClusterIssuer. The same shared
Lease, operator installation, CPU and memory limits, Job deadline, generation
checks, and cleanup rules apply. The workload runner requires the preparation
record before it can create a namespace.

Six small boundary tests pass. They reject broader inspection rules, changed
production rules, changed binding order or identity, changed allocator code,
and repeated declaration edits. The complete live workflow passed in 342.91
seconds. All 57 access checks and six admission probes passed. The workflow
covered REST and gRPC, SQL isolation and session repair, API and worker restart,
correlated telemetry, browser credentials, logout, and normal deletion. All 229
fixture output files matched repeated generation and the check after tests.
The source matched the frozen copy; the production declaration and runtime
remained unchanged. Cleanup removed the test namespace and owned resources
before it released the shared Lease.

The first run passed all 57 access checks, then failed on an admission message
expectation. Changing the probe actor from the test identity to the allocator
selected its resource-ownership rule. A public dry-run confirmed that denial.
The correction changed only the expected message. All six application image
digests matched across the two runs. The failed run has its own retained result
and cleanup record. See [the verified evidence](browser-inspection-evidence.json).

The test control namespace remains trusted, and the operator still installs
cluster policy. Connecting this fixture to restricted, unattended workload CI
remains open. Installation CNPG, actual RDS, and Sandbox checks also remain open.
The browser fixture does not publish an external connection endpoint.
