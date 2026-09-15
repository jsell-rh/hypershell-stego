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

Status: local boundary checks pass. Live admission and the complete workflow
have not yet been verified for this path. The earlier operator-run CNPG result
does not establish an unattended CI pass.
