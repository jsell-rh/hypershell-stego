# CNPG CI

The CI workflow must create a local PostgreSQL server, run the complete Gateway
browser workflow, replace the primary PostgreSQL Pod, verify retained data and
credentials, and remove all test runtime resources and volumes. A build or a
server readiness check does not pass this gate.

The operator runs `scripts/prepare-cnpg-ci.py` once with the saved jshell context.
This command installs the pinned CNPG CRDs, fixed test namespaces, namespace
roles, admission policy, and webhook trust. It starts no test Pod. It refuses to
replace existing objects. Its private journal records each requested object and
its observed UID. An incomplete installation requires inspection before repair.

The CI identity cannot change CRDs, webhooks, namespace policy, or role bindings.
The operator process receives a certificate from the existing certificate issuer.
It does not generate or patch webhook trust. CNPG 1.30.0 uses `WEBHOOK_CERT_DIR`
to select this supplied certificate. The source contract is in the pinned
[CNPG controller](https://github.com/cloudnative-pg/cloudnative-pg/blob/v1.30.0/internal/cmd/manager/controller/controller.go).

`scripts/check-cnpg-ci.py` uses only the restricted `jshell-ci` context. The shared
Lease permits one live test at a time. The operator Job has a 40-minute deadline.
The database lifetime Job has a 25-minute deadline and owns the database Cluster.
Both Jobs have zero retries and immediate cleanup after completion. Namespace
quotas limit CPU, memory, temporary storage, and database storage. Admission
policy fixes the operator image, command, namespace, environment, and executable
mount paths. Live dry-run checks must confirm both valid requests and policy
denials before the application starts.

The runner checks the exact committed source and compiler pin before use. It
retains the existing browser workflow and generation checks. Cleanup uses object
UIDs and recorded ownership. The storage controller must remove the database
volumes. CI has no permission to delete a PersistentVolume. A missing result,
unknown owner, remaining application object, or remaining volume retains the
shared Lease for inspection. It does not start a replacement test.

The reusable GitHub workflow runs for pushes and supports explicit dispatch.
Pull requests do not receive cluster credentials. Artifact selection excludes
credentials and private fixture contents. Static installation objects remain
between runs; application and database runtime resources must not remain.

The [static installation evidence](cnpg-ci-installation-evidence.json) records
45 operator-owned objects from source `dab02cf`. Independent reads verified
their selected identities, all three admission policies with no type errors,
the ready certificate, injected webhook CA, immutable configuration, and absence
of test Jobs and Pods. The installer used a frozen source copy and released its
shared Lease. Local boundary checks passed: six CI checks and 13 fixture checks.

The initial 14 Job dry-run probes passed with the CI identity. A further review
found that the database lifetime Job could change its command, environment,
probe command, and network labels. The
[regression evidence](cnpg-ci-lifetime-admission-evidence.json) confirms that all
four unwanted variants were accepted by the original policy. No Job was created.

The corrected policy pins the lifetime image and command, excludes extra code
and inputs, and prevents the lifetime Pod from selecting database network rules.
The database namespace also receives a default deny policy. Its existing
PostgreSQL policy supplies only the required database traffic permissions.

[CI 34968718717](https://github.com/jsell-rh/hypershell-stego/actions/runs/34968718717)
was canceled before execution because this correction needs its own source and
admission checks. The corrected policy was applied from frozen source `b6e0434` after the browser
run released its Lease. The repair added one network policy and changed the
lifetime Job policy. It used the recorded owner and object UIDs. Independent
reads verified the full policy specification, its completed type check, the
network deny rule, absence of runtime resources, and the released Lease.
All 18 Job admission probes passed, including denial of the four regressions.
The initial 45-object installation record remains unchanged; the repair record
identifies the added object and the changed policy generation.

The first complete restricted CNPG job in
[CI 34969545259](https://github.com/jsell-rh/hypershell-stego/actions/runs/34969545259)
failed. The operator started and both database instances became healthy. The
application Pod could not schedule until more cluster capacity was available.
It then exceeded the bounded Pod readiness wait. No application test ran.
Application Job `ee349c94-b415-4f67-8021-809fb0117feb` and its fixtures were removed.

CNPG cleanup then failed because it tried to list namespaces with the CI
identity. That identity cannot list namespaces. The corrected runner uses the
existing allocator client in its read-only mode and requires fresh evidence
that no allocations remain. It retains direct CI checks for cluster roles and
bindings. Ten focused Python checks passed. The correction does not increase
CI permissions or change the application startup deadline. Full application,
primary replacement, and automatic cleanup evidence remain required.
The ordinary API and browser runs `34969544850` and `34969544751` were canceled
before execution because this change affects only the CNPG test fixture.
The application output remains the verified `752d92e` output. Canceled and
queued runs are not passes.

The same push also queued ordinary API run `34968718133` and browser run
`34968718081`. Both were canceled before execution to avoid duplicate tests:
this change adds CI support and does not change application runtime output.
The existing JWT application runs and the complete new CNPG job remain required.
Canceled runs are not passes.

A later source review found an incorrect permission probe: `oc auth can-i create
pods/exec` checks a named Pod, not the exec subresource. The runner now uses
`oc auth can-i create pods --subresource=exec`. The explicit command returned
`no` with exit status 1 for the restricted CI identity in the operator namespace
on 2026-09-15. All nine focused Python checks passed. Named ConfigMap checks
retain their resource names. The active run uses its earlier frozen source;
its old exec probe is not evidence of subresource denial. This correction changes
only the permission check and does not start another live test.
