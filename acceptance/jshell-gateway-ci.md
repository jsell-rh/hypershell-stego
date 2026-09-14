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
