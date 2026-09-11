# Generated Kubernetes service

`TestGeneratedKubernetesServiceGatewayWorkflow` runs the generated Hypershell
API and event runtime as a Kubernetes Deployment. STEGO supplies the image build
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
- Replace the API Pod. Read the retained Gateway and deliver an update event.
- Receive correlated request logs and traces, plus request metrics, from both
  runtime instances. Check that private request and credential data is absent.

Run the bounded OpenShift check from a Linux amd64 workstation with `oc`,
Python 3, OpenSSL, and tar. This command does not build or test Go locally:

```sh
STEGO_TEST_CONTEXT=YOUR_SAVED_CONTEXT scripts/check-service-deployment.sh
```

The command creates a separate namespace with a quota. The test Job has a
30-minute deadline, a one-CPU test container with 3 GiB of memory, and a
PostgreSQL sidecar with 500 millicores and 512 MiB. The generated API has a
one-CPU and 512-MiB limit. Rollout can temporarily start a second API Pod.
No container uses privilege. The command deletes the namespace after the run
and keeps logs, source hashes, image metadata, and job status in a private
results directory.

The Job fetches the exact compiler commit, repeats generation, checks drift,
verifies dependencies, and runs static checks. It builds a static Go binary,
adds the binary and CA roots to a scratch image, and pushes the image to the
namespace's internal registry repository. Its registry credentials come from
its own mounted service-account token and are never printed. The generated API
ServiceAccount has no token mount or RBAC grant.

The application uses a separate database login with table and sequence access.
PostgreSQL uses native verified TLS and SCRAM authentication. The Kafka protocol
fixture uses mutual TLS. The OTLP collector uses verified TLS. Projected Secret
files supply the database URL, key pairs, and public trust roots.

The fixture is not a production Kafka broker. The separate `service-image` CI
job builds the generated Containerfile itself. The cluster check constructs
the same runtime file set with `oc image append`; it does not run Docker or a
privileged image builder. Public ingress, certificate renewal, production
broker operation, capacity, deployment migrations, and separate domain
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
