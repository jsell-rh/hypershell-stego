# Generated Kubernetes service

`TestGeneratedKubernetesServiceGatewayWorkflow` runs the generated Hypershell
API and event runtime as a Kubernetes Deployment. It runs the generated Gateway
identity worker in a separate Deployment. STEGO supplies the image build
files, resource renderer, runtime TLS, mounted configuration, health probes,
network policy, process limits, and telemetry. Hypershell supplies its domain
source directories and test environment references.

The test checks these behaviors:

- Create a Gateway through the generated HTTPS SDK. Check its KSUID, namespace,
  database ID, timestamps, and owner grant.
- Retrieve the same Gateway through HTTPS and gRPC. Deny another user and
  filter that user's list.
- Receive the committed event through the generated runtime.
- Reject the final event write and verify rollback of the Gateway, database,
  and owner grant.
- Reconcile the Gateway identity through a generated worker Deployment and a
  real Keycloak Pod. Check the browser client and current identity condition.
- Stop the controller, change identity state through HTTPS, and start a new
  worker Pod. Check that its initial scan repairs the state.
- Replace the API Pod while the controller runs. Read the retained Gateway,
  check identity repair after reconnect, and deliver an update event.
- Stop the controller, drain its outbox records, and record the Kafka end
  offset. Require the final image update event to be at or after that offset.
  An earlier identity update cannot satisfy the event check.
- Receive correlated logs and traces, plus metrics, from both API instances and
  both worker instances. Check that private request and credential data is absent.

Run the bounded OpenShift check from a Linux amd64 workstation with `oc`,
Python 3, OpenSSL, and tar. This command does not build or test Go locally:

```sh
STEGO_TEST_CONTEXT=YOUR_SAVED_CONTEXT scripts/check-service-deployment.sh
```

The command creates a separate namespace with a quota. The test Job has a
30-minute deadline, a one-CPU test container with 3 GiB of memory, and a
PostgreSQL sidecar with 500 millicores and 512 MiB. The generated API has a
one-CPU and 512-MiB limit. Rollout can temporarily start a second API Pod.
The Keycloak fixture has a one-CPU and 1-GiB limit and a ten-minute Pod deadline.
The generated worker has a one-CPU and 512-MiB limit. The namespace CPU limit
is six CPUs to permit the API rollout, worker, and fixtures.
No container uses privilege. The command deletes the namespace after the run
and keeps logs, source hashes, image metadata, and job status in a private
results directory.

The Job fetches the exact compiler commit, repeats generation, checks drift,
verifies dependencies, and runs static checks. It builds static Go binaries,
adds each binary and CA roots to a scratch image, and pushes both images to the
namespace's internal registry repositories. Its registry credentials come from
its own mounted service-account token and are never printed. The generated API
and worker ServiceAccounts have no token mount or RBAC grant.

The application uses a separate database login with table and sequence access.
PostgreSQL uses native verified TLS and SCRAM authentication. The Kafka protocol
fixture uses mutual TLS. The OTLP collector uses verified TLS. Projected Secret
files supply the database URL, key pairs, and public trust roots.

The Keycloak fixture uses the pinned image and test realm from the existing
Docker checks. It uses standard server mode with a local test database, local cache,
TLS 1.3, an explicit hostname, and no HTTP listener. The test checks port 8080 inside
the Pod, so network policy cannot conceal an open plaintext listener. Its
network policy permits fixture and worker requests on port 8443 and denies
outbound connections. It has no service-account token. Its writable
container filesystem and test realm are not production configuration. Keycloak
requires a separate production image and durable database; see the
[Keycloak container guide](https://www.keycloak.org/server/containers).

The identity worker uses a generated main, image, Deployment, and health probes.
Hypershell supplies provider setup through `internal/gatewayidentityapp.Run`.
Its domain controller uses the generated gRPC client, reconciliation runtime,
scans, conditions, and telemetry. The worker has no inbound network access.
Its exec probes connect only to its loopback endpoint. Readiness requires an
active queue that can accept work. It does not certify all domain resources.

The worker has one replica and uses `Recreate`. Planned rollout does not start
a second worker before the first Pod stops. This is not a distributed lease or
a fence for an unreachable node. Multiple active workers remain outside this
check. The acceptance test uses race detection; the deployed images do not.

The fixture is not a production Kafka broker. The separate `service-image` CI
job builds both generated Containerfiles. The cluster check constructs
the same runtime file set with `oc image append`; it does not run Docker or a
privileged image builder. Public ingress, certificate renewal, production
broker operation, capacity, deployment migrations, and other domain
controller deployments remain outside this check.

The first pinned run passed on jshell on 2026-09-11. The application test took
10.71 seconds; its race-enabled package took 11.757 seconds. Its image digest
was `sha256:7623241fcd22fcdcf19425a628372ee0bea36c9efc46b651f1f31acd295945d3`.
Repeated generation and the post-test check preserved 112 generated, state,
and dependency hashes. The test Job completed, and its namespace was deleted.

That run found a common Kafka credential-file gap. STEGO component version
1.0.4 now permits private projected files with read-only group access. It still
rejects execute bits, group-write access, and other access. The variant does
not copy or change the mode of mounted credentials.

The initial image publisher failed certificate verification and then omitted
its entry point. Those failures are retained in the test record. The publisher
now supplies system and cluster CA roots and validates the image entry point,
user, architecture, and digest. A first run of the wrapper also exposed a race
between Job creation and the Pod lookup; cleanup succeeded. The wrapper now
waits for the Job to create an active Pod before it checks readiness.

The corrected wrapper passed from a fresh namespace on 2026-09-11. The Gateway
test took 11.57 seconds; its race-enabled package took 12.621 seconds. The fresh
compiler build produced the same 112 file hashes and the same image digest as
the first successful run. The Job reached `Complete`, and namespace deletion
was verified. The generated Containerfile build has its own CI job.

The first identity extension failed when it supplied a Pod deadline inside a
Deployment. Kubernetes forbids that field in a ReplicaSet template. The fixture
now uses a single Pod with `restartPolicy: Never` and a ten-minute deadline.
The failed run and its logs are retained. Its namespace was deleted.

The second identity run passed real Keycloak creation, controller restart,
and API Pod replacement. It failed the final telemetry check after the longer
setup filled the test collector's 64-batch metrics buffer. The test now exports
metrics every ten seconds to reduce buffer use during provider startup. The test
still requires metrics from both API instances. This changes only the test configuration.

The same run showed that Keycloak development mode opened port 8080 despite
`--http-enabled=false`. The network policy blocked that port. The fixture now
uses standard server mode and checks that the local port is closed.

The earlier full [CI run 34619444307](https://github.com/jsell-rh/hypershell-stego/actions/runs/34619444307)
passed for `52edc0b`. That result covers the prior API deployment change.
Later identity-test commits require their own results.

CI run 34621260515 rejected stale compiler state after the Kafka test module
became a direct dependency. Cluster generation reproduced the new state twice.
The recorded state now includes that module-file hash. Generated service files
did not change. The failed CI run remains a failure.

The third run reached Pod readiness but failed its single HTTPS request to the
Keycloak Service. The Service endpoint can lag Pod readiness. The fixture now
waits at most 30 seconds for a successful verified HTTPS request. It does not
bypass certificate verification. The local listener check requires an open
HTTPS port before it tests that the HTTP port is closed. Its exec permission
is limited to the named Keycloak fixture Pod.

The complete identity extension passed on jshell on 2026-09-11 with application
commit `6fe35d41fc58dfd38dfcc0204785ce270cdee06b` and compiler `ae4f1a2`.
The application test took 100.05 seconds; the race-enabled package took 101.094
seconds. It passed identity creation, controller restart, API Pod replacement,
the final event offset check, and both API instances' telemetry checks. The
Keycloak Service needed two verified HTTPS requests before it was ready.
The local port check confirmed that HTTPS was open and HTTP was closed.

Both generation passes and the post-test check preserved all 112 output,
state, and dependency hashes. Those hashes also match the local checkout.
The service image retained digest
`sha256:7623241fcd22fcdcf19425a628372ee0bea36c9efc46b651f1f31acd295945d3`.
The Job reached `Complete`. All four namespaces from the identity extension
were deleted. Private fixture files were removed from the local results.
The successful run is in `/tmp/stego-service-results.0pgacZSe`.

No compiler or domain runtime change was needed for that identity extension.
The generated worker image, probes, and Deployment were added after that run.
They require a separate cluster result.

The generated worker extension passed on jshell on 2026-09-11 with compiler
`cb0326dd659e3904ed17654e09778564b8660fa7`. Application commit
`56759c46649e2239929409329bc69718250c5ea5` records the worker source and fixtures.
The test took 108.26 seconds; its race-enabled package took 109.305 seconds.
It passed identity creation, worker Pod replacement, API Pod replacement,
access filtering, atomic owner grants, event rollback, and event delivery.
Both API instances and both worker instances supplied correlated logs and
traces, plus metrics. The checks found none of the selected private values.

All 117 generated, state, and dependency hashes matched across both generation
passes, the post-test check, and the local checkout. The service image digest was
`sha256:3b81f1f499683f7609aa2a7cd6bb2c3c60e4941ff61653be053ff152026606dc`.
The worker image digest was
`sha256:ae1d7ee318e21c2af2ceceba11d2f02cca99715cf357e6c7d42b48b4c5f432b1`.
Both images had the required non-root user and entry point. The Job reached
`Complete`. Namespace deletion and removal of private fixture files were verified.
The evidence is in `/tmp/stego-service-results.fzc29X8u`.

Review found that the fixture needed scale-subresource access. Before application
tests started, the fixture Role received `get`, `patch`, and `update` access to
`deployments/scale` for `hypershell-gateway-identity` only. The committed fixture
contains that rule. The retained initial archive has the earlier fixture Role;
`worker-scale-role.json` and `fixture-amendment.txt` record the applied change.
The runtime source and generated inputs did not change during the test.

[Compiler CI](https://github.com/jsell-rh/stego/actions/runs/34624175055) passed
for the pinned compiler, including race tests and the vulnerability check.
[Full variant CI](https://github.com/jsell-rh/hypershell-stego/actions/runs/34624732979)
is tracked separately from this cluster result. Distributed worker exclusion,
production deployment operations, and capacity remain open work.
