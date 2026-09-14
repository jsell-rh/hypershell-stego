# Gateway API gate with the restricted CI identity

`scripts/check-jshell-gateway.py` runs the Gateway API gate in `stego-ci`.
GitHub workflow `jshell-gateway.yml` supplies the private CI kubeconfig.
The runner uses the explicit `jshell-ci` context. It does not use the operator
credential, install cluster resources, or start kind.

The required tests cover Gateway IDs and shapes, the atomic owner grant and
events, filtered lists and denied requests, event delivery, REST, gRPC, API
restart, watch events, local database selection, registration, and migration.
The catalog workflow also checks cluster placement and cleanup dependencies.
Each required test must have an explicit pass event in the Go JSON output.
A missing or skipped test is a failure. The complete application suite remains
separate and required.

The runner freezes tracked source and records its hashes. It verifies Go
modules and applies the pinned compiler twice. The file lists and hashes must
match the committed output and the output after testing. Each test process
uses the race detector. The Job has one test CPU, 3 GiB test memory, and a
20-minute deadline. Its private PostgreSQL sidecar has half a CPU and 512 MiB.
Both containers have bounded storage and use verified database TLS. No API
token is mounted in the Pod.

GitHub serializes its jshell runs. The shared cluster Lease also excludes the
operator-run CNPG, count, and service fixtures. Cleanup checks ownership, uses
UID and resource-version preconditions, stops the Job, and waits for its Pods
and fixture objects to disappear. Cleanup failure retains the Lease for
operator inspection. Test credentials and private keys are not stored in the
result directory or artifact.

The CI kubeconfig expires after one hour. An operator must renew it before a
later run. Automatic credential renewal remains open. An absent or expired
credential fails the gate; it does not skip the tests.

## Fixture corrections

The watch fixture now gives its controller an exact `observe.sandbox-count`
grant for its own cluster. It also proves that a Gateway owner cannot change
that count. Production access checks are unchanged.

The catalog fixture supplies the required cluster ID for CNPG registration.
It checks that a deleted Gateway still blocks parent deletion while workload
cleanup is pending. It then records a versioned cleanup observation with an
exact grant. The same check applies to database cleanup before cluster
deletion. These are API cleanup observations in a fixture with no Kubernetes
workloads. The separate live CNPG and rendered browser gates prove actual
resource removal.

## Old workload jobs

The old kind installer is removed from `scripts/check-workload.sh`. The CNPG
selector now requires an explicit operator context and runs the bounded jshell
fixture. The other four selectors report failure until their replacement
fixtures are available. They do not return success or skip a required test.
The old Sandbox job no longer changes host VM devices before that failure.

The database selector still names the removed deployment-backed provider.
It must be replaced with external PostgreSQL and CNPG evidence. Gateway and
Sandbox workflow conversion remains open. Existing test code stays available
for that conversion. This work does not establish a passing full CI result.

## First restricted-identity run

The unchanged watch fixture failed in Job
`stego-ci/gateway-api-3edadb2a0720` on 2026-09-14. The count request returned
gRPC PermissionDenied. The other 11 required tests passed. The package took
33.618 seconds. The source archive SHA-256 was
`b8e00db4e661dd2badc268ddd4b8e3b330c022fb73feb9461285109b5773bbaa`.
It contained 837 tracked files. Evidence is in
`/tmp/hypershell-ci-gateway-baseline`.

The committed output and both compiler runs had the same 231 file hashes.
The failed test stopped the script before its final hash check. This run does
not prove unchanged output after testing. The Job, Pods, two Secrets, and
ConfigMap were removed. The shared Lease was released before the GitHub run.

## Passing GitHub run

[Run 34883281239](https://github.com/jsell-rh/hypershell-stego/actions/runs/34883281239)
passed for `e491f85e39b8f383f2c8c0ab8460696ed2bba357`.
All 13 required tests passed under race detection in 47.595 seconds. The
catalog test took 7.87 seconds; the watch test took 6.53 seconds. No required
test was skipped. The bounded cluster Job took 4 minutes 50 seconds, including
compiler and application builds.

The workflow installed the pinned OpenShift client, read the restricted CI
credential, created Job `stego-ci/gateway-api-2519005f3b01`, collected its
result, and removed its fixture. No operator credential was supplied to
GitHub or the test Pod. GitHub also removed its local kubeconfig and uploaded
the evidence artifact `gateway-api-34883281239-1`.

All 231 committed, first-generation, second-generation, and post-test file
hashes matched. The generated archive also matched all 231 checkout files.
The frozen source manifest covered 839 files and has SHA-256
`2fe78844735a99b5dba7c8fe0214bb5e9ecd5d37f3171811a6466555b5e7608a`.
Only this evidence document changed after the test. The downloaded artifact
is in `/tmp/hypershell-ci-gateway-34883281239`.

The Job reached Complete. CI checked that the Job, its Pods, both Secrets,
and the ConfigMap were absent before it released the shared Lease. Separate
reads confirmed the Job and named fixture objects were absent and the Lease
holder was empty. The safe summary is in
[the committed result](jshell-gateway-ci-evidence.json).

The separate full application run `34883281256` still has failed workload
jobs. Those failures identify fixture conversions that remain required.
This passing API gate does not replace the full application, rendered browser,
external PostgreSQL/RDS, or Sandbox acceptance requirements.
