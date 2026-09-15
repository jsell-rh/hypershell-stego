The browser CI workflow uses the restricted `hypershell-ci` identity and the
fixed `stego-service-ci` namespace. The operator installs the namespace, quota,
admission policy, and generated cluster resources before CI runs. CI cannot
create cluster roles or change admission policy. The test source remains
trusted: it can start workers and request their scoped tokens in this namespace.

Prepare the inspection source with `scripts/prepare-browser-inspection.py`.
From that frozen directory, run `scripts/prepare-browser-ci.py --context
<operator-context> --issuer <existing-cluster-issuer> --results <new-directory>`.
The installer refuses existing resources and records each created UID. A failed
installation needs operator inspection; it is not replaced automatically.

An immutable ConfigMap records the namespace UID, file group, and all six
generated cluster manifests. Before a Job starts, CI checks all 18 live cluster
resources against that record. It also renders the cluster scope from its own
frozen source and requires identical bytes. A policy change needs a new operator
installation. An operator credential cannot be used in this CI path.

The CI identity can reset only the named `service-check` Role. Kubernetes RBAC
checks prevent it from granting rights it does not have. The runner adds exec
access for the exact test Pod name. The test Pod cannot change its own Role.
The same Lease serializes browser and API tests. The Job has one Pod, no retry,
a 30-minute deadline, a one-hour cleanup limit, and CPU, memory, and storage
limits. Privileged containers and persistent volumes are not permitted.

Cleanup stops the test Job and generated processes first. It uses the generated
allocator to remove owned Gateway and retained-state namespaces, including
orphan cluster bindings. Inventory must be complete, versioned, and bounded.
Ownership conflicts, incomplete reads, and remaining resources keep the Lease.
Cleanup then removes labelled test data and verifies that the operator's
installation remains. It does not remove finalizers to force deletion.

The first preflight passed the restricted identity and live manifest checks.
Admission then rejected the test Job because its pod-level non-root setting
was implicit. The fixture now sets it explicitly. The Job cleanup limit is also
one hour. The first 15 actual admission and access checks passed. They include
rejected parallel Jobs, excessive deadlines, foreign identities, privileged
containers, lasting tokens, foreign Secret reads, and installation writes.
No Job ran during these preflight checks. Installation CNPG, actual RDS, and
Sandbox checks remain open.

Use `scripts/check-browser-ci-recovery.sh` from a frozen inspection source to
check cleanup after failure. Set the explicit CI context and a new results
directory. The check creates two allocations through the generated allocator,
then starts one small Job that exits with code 23. It removes the Job and its
Pod before it calls the normal CI cleanup path. The result must confirm that
both allocations and labelled test data are absent and that the operator's
installation remains. This check uses the same shared Lease. The live check
passed at `de07bff`: the Job exited with code 23, its Pod was removed, both
allocations were removed, and all 16 access checks passed. The frozen source
matched all 867 files in that commit. The shared Lease was released.

The first GitHub run passed the complete application workflow in 335.05 seconds,
then failed during host cleanup. The test's cleanup had deleted the allocator
service account. Host cleanup could no longer request its token. The Job and
Pods were gone, but the Lease and labelled fixture data remained.

Test cleanup now keeps service accounts. The test and CI roles also lack
service-account deletion rights. An actual denied-delete probe raises the
access check count to 16. The operator restored the missing allocator account
and removed the CI deletion right. Restricted CI cleanup then removed the
remaining data, verified the installation, and released the Lease. This repair
does not change the failed GitHub run into a passing run. The new
[browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34936017009)
passed the complete workflow in 331.54 seconds and passed host cleanup. All
867 source files and 229 generated files match the committed source and frozen
records. All 16 CI probes, 57 application access checks, and six admission probes
passed. The operator installation remains; test data and allocations are absent.
The [API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34936017005)
also passed all 30 required tests. Its full inventory of 867 source files and
228 generated files matches the commit. The Job, Pods, and fixtures are absent. See the [verified evidence](browser-ci-evidence.json).

The fixed namespace retains six test image streams. Registry retention is
outside this cleanup proof. The browser fixture has no external connection
endpoint; its screenshots do not prove external connectivity.

Both jshell workflows use `queue: max` in the same concurrency group. New pushes
must not replace a required pending API or browser run. One run remains active
at a time, and the shared cluster Lease remains the second guard. GitHub documents
a limit of 100 pending entries for this setting; a full queue rejects new runs.
See [the queue contract](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).
An expired CI credential still stops a run. A queued run is not a passing check.

During the queue transition, both new push runs were cancelled before a Job
started, when the older API workflow became active. The API provides no reason
for the cancellation. The same pushed commit now has new browser and API
dispatches. The old run uses the default queue setting; new runs use `queue: max`.
Do not treat this transition as proof that the new queue preserves all runs.

After the older API run finished, the namespace recovery browser run started
and passed. Its separate API run is now active. Both runs for the SQL cleanup
denial candidate remain queued. See the [namespace recovery result](browser-namespace-replacement-evidence.json).
