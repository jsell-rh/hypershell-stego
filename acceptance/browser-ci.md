The browser CI workflow uses the restricted `hypershell-ci` identity and the
fixed `stego-service-ci` namespace. The operator installs the namespace, quota,
admission policy, and generated cluster resources before CI runs. CI cannot
create cluster roles or change admission policy. The test source remains
trusted: it can start workers and request their scoped tokens in this namespace.

The `jshell-ci` GitHub environment supplies the short-lived credential when the
job starts. The runner requires at least 35 minutes of remaining time before
it acquires the Lease. An operator still renews the environment secret. See
the [credential timing contract](ci-credentials.md).

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
and passed. Its separate API run also passed all 30 required checks. All 870
source files and 228 generated files match that commit, and its Job, Pods, and
fixtures are absent. The SQL cleanup denial candidate passed its API run with all 30 required
checks, matching source and generated files, and complete cleanup. Its browser
run failed in the permission fixture before the denial assertion. Host cleanup
passed. The pending encryption browser run was cancelled before it started
because it had the same fixture error. Commit `70b2dd7` corrects the fixture and
includes the encryption test. Its complete browser and API checks are queued. See the [namespace recovery result](browser-namespace-replacement-evidence.json).

The encryption candidate `ee1f23e` passed its separate API run with all 30
required tests. All 872 source files and 228 generated files match the commit.
Generation and cleanup passed. This compiles the encryption test but does not
execute its storage assertions. The corrected `70b2dd7` browser run is active;
its API run is queued. The earlier encryption browser run remains cancelled.

The corrected `70b2dd7` [complete browser workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/34941181554)
passed in 384.8 seconds. It proves the SQL denial and recovery path and stored
credential encryption after namespace recovery. All 872 source files, 229
generated files, 16 CI access checks, 57 application access checks, six admission
probes, and 18 operator resources were verified. Cleanup passed. The screenshot
shows the healthy Gateway; its loading connection panel does not prove an
external endpoint. The API run for this source remains active.

The separate API run for `70b2dd7` also passed all 30 required tests. Verification
covered all 872 source files and 228 generated files. Generation records match;
the Job, Pods, and private fixtures are absent. Both the API and full supplied
PostgreSQL browser gates now pass for the SQL denial and encryption source.
The installation CNPG test remains a separate required check.

Full CI run [34942025954](https://github.com/jsell-rh/hypershell-stego/actions/runs/34942025954)
for `3757c27` completed. The core suite, ordinary browser suite, web console, and
service image jobs passed. The overall result is failure because the CNPG and
Sandbox jobs failed. The operator-assisted CNPG run is separate evidence.

The later full CI run `34943002657` failed `TestCountAccessLossStopsWatch`: a
denied count write could cancel the watch before its change acknowledgement
returned. The test now permits that cancellation while it still requires the
controller's final result to be `PermissionDenied`. The single focused test
passed. Full CI for the correction remains required. No runtime changed.

The supplied CNPG application workflow passed in 462.01 seconds. Its final
cleanup read failed; separate read-only checks confirmed complete cleanup. The
[evidence](cnpg-installation-evidence.json) preserves the nonzero runner result,
the application pass, and the manual cleanup checks.

The cleanup retry change in `2bc2b2c` passed the
[complete supplied PostgreSQL browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34947486151)
in 396.86 seconds. Verification covered 882 source files, 229 generated files,
16 CI access checks, 57 application access checks, and six admission probes.
All three generation records matched. SQL cleanup denial, namespace recovery,
credential encryption, session isolation, and automated cleanup passed. The
Job and test Pods are absent, and the shared Lease is free. This run used the
supplied PostgreSQL fixture; it does not replace the separate CNPG result.

Full CI run [34947673554](https://github.com/jsell-rh/hypershell-stego/actions/runs/34947673554)
completed at source `ee79099`. Core acceptance passed in 1357.045 seconds after
the count access-loss correction. Ordinary browser, console, and service-image
jobs also passed. The overall run failed on the unfinished CNPG and Sandbox
jobs. This result does not include the later availability or viewer changes.

The shared Deployment availability change passed the
[complete browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34950339472)
at `3f188b2` with compiler `5e9c89d`. The application took 387.32 seconds.
Verification matched 886 source files, 230 generated files, three generation
records, all access and admission checks, and automated cleanup. Namespace
recovery, SQL isolation, encryption, and session checks passed. The screenshots
still have no public connection command. That source does not include the later
viewer and live-account cleanup changes. See
[the verified record](workload-availability-evidence.json).

The combined viewer and account
[browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34951393842)
passed at `10a0827` in 395.99 seconds. It proves retained viewer membership,
filtered reads, denied operations, access removal, and cleanup of three live
automation accounts on Gateway deletion. SQL isolation and recovery,
encryption, session checks, and automated cleanup passed. Verification matched
887 source files, 230 generated files, all generation records, and the access
and admission checks. The Job and Pods are absent. See the
[verified evidence](browser-viewer-recovery-evidence.json). External connectivity
and the later environment-backed CI credential change remain separate gates.

The [environment-backed browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34952975989)
passed at `f900d5a` with compiler `5e9c89d` in 380.37 seconds. Verification matched
890 source files, 230 generated files, all three generation records, and all
access and admission checks. Viewer recovery, denied writes, access removal,
three live account cleanups, SQL isolation, encryption, and automated cleanup
passed. Namespace recovery took 44.26 seconds. Controller Pods and SQL identities
did not change. The Job and Pods are absent. See the
[verified result](browser-environment-credential-evidence.json). This closes the
live browser check for the CI environment credential change. It does not close
the public endpoint, CNPG, RDS, or Sandbox requirements.

The [route-composition browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34960101315)
passed at `8551ad3` with compiler `1edd407` in 407.18 seconds. Verification matched
897 source files, 230 generated files, all generation records, 16 CI access
checks, 57 application access checks, six admission probes, and 18 operator
resources. Namespace recovery took 54.37 seconds and retained controller Pods,
SQL identities, credentials, and keys. Viewer access, denied requests, account
cleanup, SQL cleanup denial and recovery, encryption, and session checks passed.
Automatic cleanup passed. Independent reads found no browser Job, Pods, or
private test fixtures. The next serialized API Job holds the Lease. The images
show a healthy Gateway and an empty account list; they do not verify a public
connection command. See [the verified record](browser-route-composition-evidence.json).
The API run and later pool-metric source have separate acceptance results.

The separate [route-composition API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34960101313)
passed all 30 required tests at `8551ad3`. Verification matched 897 source files,
229 generated files, and all generation records. Its Job, Pods, and private
fixtures are absent. The next serialized API Job holds the Lease. Full CI
`34960101322` passed core acceptance in 1269.092 seconds, ordinary browser tests,
the console, and the service image. The overall result remains failure because
the CNPG and Sandbox jobs failed. See [the verified API record](api-route-composition-evidence.json).

The [PostgreSQL client telemetry change](postgres-client-observability.md) at
`2af1b46` has verified passes for API `34962176232` and browser `34962176339`.
The [browser record](postgres-telemetry-browser-evidence.json) includes complete
SQL signal checks across worker restart and cleanup denial and recovery.
Full CI `34962176260` passed core acceptance, ordinary browser checks, console,
and the service image. Its overall result is failure because the Sandbox and
CNPG jobs still need installation fixtures and restricted runners. Its compiler
is `f2b09c0`. The earlier pool-metric
runs remain in the queue or active; no required live test was canceled.

The [complete browser pool run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34961014607)
passed at `916f3a7` in 366.3 seconds. Verification matched 899 source files,
231 generated files, all generation records, 16 CI access checks, 57 application
access checks, six admission probes, and 18 operator resources. All three
console pool checkpoints retained the configured limit and had distinct runtime
identities. Namespace recovery took 44.35 seconds. Access rules, SQL isolation,
cleanup denial and recovery, account cleanup, encryption, and automatic cleanup
passed. Independent reads confirmed that browser Jobs, Pods, and private test
fixtures were absent. The next serialized API Job holds the Lease.

The reviewed images show a healthy Gateway and an empty account list. They do
not verify a public connection command. Full CI `34961014648` passed core
acceptance in 1223.434 seconds, ordinary browser tests, console checks, and the
service image. Its overall result remains failure because the CNPG and Sandbox
jobs failed. See [the verified pool record](database-pool-browser-evidence.json).
The later SQL client telemetry API run is active; its browser run remains queued.

The public Gateway profile needs a bounded update to two existing ClusterRoles.
Use the [public permission plan](browser-ci-public-permissions.md) to compare
saved installation state with a fresh frozen render. The live operator update
and complete public workflow remain required.
