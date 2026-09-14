# Browser Gateway workload acceptance

## Current CNPG gate

The complete CNPG browser Gateway workflow passed on 2026-09-14 UTC. The test
passed in 409.01 seconds; the race-enabled acceptance package passed in
410.057 seconds. The Job reached `Complete`, and the test wrapper exited zero.

The tested application revision is
`7f46379f382de6e2f0f2d262e1f880f65ba8af0a`. It uses STEGO
`158f448545f253cd582035aff3ec51c1302ef6ac`. STEGO's full checks passed in
[CI run 34852254025](https://github.com/jsell-rh/stego/actions/runs/34852254025).

The live workflow proved:

- Browser Gateway creation, grants, REST and gRPC access, and event delivery.
- Two healthy Gateways on one local CNPG server, with separate restricted SQL
  logins, verified TLS, and denied access to other and system databases.
- Gateway provider data recovery after Pod replacement. All four generated
  workers were then replaced; SQL isolation and credential identities persisted.
- Eighteen worker access checks and three denials from the expected generated
  admission rules. Public dry-run requests did not change stored objects.
- Worker metrics and correlated logs and traces before and after replacement.
- API, console, and provisioner replacement; service credential issuance, use,
  reload, revocation, and deletion; session renewal, key rotation, and sign-out.
- REST Gateway deletion with actual removal of its SQL database, login, keys,
  namespace, and cluster bindings. The other Gateway and shared server remained.

All 818 tracked files matched the frozen source before this evidence update.
All 230 generated and build-record hashes matched both generation passes,
the post-test files, the collected archive, and the checkout. No generated
file changed during the tests.

Results are in `/tmp/stego-service-results.vgYuxDm1`; the frozen source is in
`/tmp/hypershell-cnpg-browser-p4dcrnsk`. Cleanup checks found no remaining CNPG
installation resources, allocated or test namespaces, test cluster roles,
bindings, admission policies, database volumes, or private launch files.

External PostgreSQL provisioning, removal of the old deployment runtime, and
conversion of the older CI fixtures remain open. This gate does not establish
production capacity or complete workload network isolation.

The current test uses two Gateways and one local CNPG server. It starts seven
generated Deployments, including the namespace allocator and three resource
workers. The database worker has no Secret access. The Gateway worker reaches
PostgreSQL through a generated network peer for its database allocation profile.

The host installs a checksum-pinned CNPG operator in a separate namespace. Its
namespaced write permissions are bound by the generated allocator. Its watches
and admission webhooks select the one test database namespace. The operator Deployment
starts after the allocation is ready. A separate Job owns the Deployment. Its
30-minute deadline and immediate TTL cleanup limit the operator lifetime if the
host exits. The installer refuses existing resources and records UIDs for cleanup.

The SQL checks require two distinct restricted logins, verified TLS, denied
access to other databases, stable object identities across worker replacement,
and removal of one Gateway without removal of the other or the shared server.
The current application result is recorded above.

## Earlier CNPG attempts

The first attempt stopped at the component schema. The next attempt passed
repeat generation, controller race checks, image builds, and the local placement
checks. Browser setup then stopped because the fixture tried to change an
existing database ID. The fixture now supplies its final ID at creation.
Both attempts and their CNPG resources were removed. Neither is a workflow pass.

The third attempt created the namespace allocation and passed the denied
database-provider write check. CNPG then stopped because its certificate code
requires a Deployment owner. The wrapper now preserves that Deployment and
uses a separate lifetime Job. The third attempt is also a failure.

A small jshell check used the same lifetime Job template with a five-second
deadline. Kubernetes removed both the Job and its owned Deployment after
37.5 seconds, including Pod termination. The check namespace was then removed.
Evidence is in `/tmp/stego-cnpg-lifetime-a2m2xe2t`. This checks the lifetime
mechanism.

The fourth attempt started CNPG with the generated namespace permissions. Both
Gateways became healthy. Both SQL logins passed verified TLS, distinct database
ownership, and denied access to the other Gateway and system databases. The
browser-created Gateway passed verified RPC, denied requests, and provider data
recovery after Pod replacement.

That attempt then stopped because its admission test required HTTP 403, while
the server returned 422. Kubernetes uses `Invalid` when a validation rule omits
an explicit reason ([admission policy reference](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)).
The test now requires the expected generated policy name and rule message in a
bounded response to a public dry-run request. It also checks that the stored
objects did not change. An unrelated validation failure cannot pass this check.
Production Kubernetes errors still omit response bodies. The complete restart
and deletion gate remains pending; the fourth attempt is not a full pass.

That run also exceeded the old 45-second namespace teardown limit. Its database
namespace retained a CNPG Database finalizer during deletion. Teardown now
removes Gateway namespaces first, waits for owned CNPG Database resources and
the Cluster to disappear, and removes the database namespace last. Each phase
uses bounded calls. The operator remains available until database namespace
removal. This fixture cleanup does not establish the normal API behavior for
deletion of a shared database server.

The fifth attempt repeated initial readiness, SQL isolation, and RPC recovery.
The namespace and quota probes matched their generated admission rules. The
identity-change probe stopped at RBAC because the allocator has no namespace
patch permission. A separate server dry-run with the permitted test identity
reached the ownership policy and returned its expected immutable-identity rule.
The workflow now uses that identity for this one public dry-run probe. A separate
access check still requires namespace patch denial for the allocator. The fifth
attempt is not a full workflow pass.

The current test needs Python with PyYAML on the host. Use the command below
with the saved jshell context. The current wrapper installs the temporary CNPG
operator; the operator details in the historical section do not apply.

## Earlier deployment-provider evidence

The result below is historical. It does not prove the current CNPG workflow.

The complete browser Gateway workflow passed on 2026-09-12 UTC with STEGO
`00b270db3934b88df1e451716d8547da1f7089e2`. The test passed in 304.14 seconds.
The race-enabled acceptance package passed in 305.184 seconds. Contract checks
passed in 1.056 seconds. The diagnostic privacy check also passed.

The frozen source is `/tmp/stego-browser-workload-xgzyjohu/application`.
Results are in `/tmp/stego-service-results.LRNjwUmR`. The Job reached `Complete`
with exit zero. All 225 generated, state, and dependency hashes match both
passes, the post-test output, and the checkout. All changed test source matches
the frozen copy. The screenshot after account deletion shows the actual
`rendered-browser-workflow` Gateway as Healthy with no service accounts.
All five owned namespaces and the test and Gateway cluster RBAC resources
were removed. Their absence was verified.

## Behavior

The console creates the Gateway. The API commits its placement, owner grant,
and event. REST and gRPC reads enforce access rules, including filtered lists
and denied requests. The generated event runtime delivers the creation event.

Three generated workers reconcile the database, identity, and Gateway workload.
The test supplies placement inputs and controller grants. It does not write
Gateway readiness. Each Gateway receives a real PostgreSQL Deployment, volume,
verified database TLS, identity client, and OpenShell Gateway Deployment.
The test requires current controller observations and a ready Gateway Pod.

The browser-created Gateway then passes verified TLS RPC checks. Invalid
identities receive `Unauthenticated`; an ungranted user receives
`PermissionDenied`. The owner creates and reads a provider record. The test
replaces the Gateway Pod, requires a different Pod UID, and reads the same
provider record. No upstream AI request is made.

The browser creates a service account for this same Gateway and captures the
one-time credential in a private file. After removal of that file, the test
replaces the generated account provisioner Pod. The credential obtains a real
identity-provider token and uses it to read the provider record from OpenShell.
Audience, roles, subject, credential privacy, reload, revoke, and account delete
checks pass. Revoke prevents new token issuance; this check does not claim
immediate revocation of an already issued access token.

API and console replacement, session key rotation, renewal, confirmed identity
provider sign-out, event delivery, and correlated telemetry checks also pass.

## Test profile

Use a fixed source copy:

```sh
STEGO_TEST_CONTEXT=default/api-jshell-8u58-p3-openshiftapps-com:443/johnsell \
STEGO_TEST_BROWSER_DEPLOYMENT=1 \
STEGO_TEST_BROWSER_WORKLOAD=1 \
STEGO_TEST_GATEWAY_CLUSTER_ISSUER=acp-hypershell-test-ca \
bash scripts/check-service-deployment.sh
```

The test uses the existing test ClusterIssuer. The jshell cluster also has the
`agents.x-k8s.io` Sandbox CRD. This check does not install or change cluster
operators, runtime classes, or issuers. It creates no Sandbox workload.

The root test namespace permits six Pods, eight CPUs, and 8 GiB of memory.
Each generated API, console, and provisioner Deployment has a one-CPU and
512 MiB limit. The test container has one CPU and 3 GiB; Chromium has one CPU
and 1536 MiB. The Job has a thirty-minute deadline and the Go acceptance command
has ten minutes. The three generated controller processes run inside the test
container. They are not separate worker Deployments in this check.

Each of four owned workload namespaces permits two Pods, one CPU, 1 GiB of
memory, 512 MiB of ephemeral storage, and 2 GiB of volume storage. The workload
images require fixed non-root UIDs. A namespace RoleBinding permits only the
selected workload ServiceAccount to use OpenShift `nonroot-v2`. Containers are
not privileged. A temporary test ClusterRole permits the controller operations
and delegation of the Gateway roles. It has no wildcard or `escalate` grant.
The wrapper also removes owned resources if the test exits before its cleanup.

The API, console, and provisioner image digests match the
[prior deployment check](rpc-deployment.md). The OpenShell Gateway image is
`quay.io/opendatahub/odh-openshell-gateway:v0.0.109-rhaiv.0@sha256:a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd`.

The default account test retains its explicit readiness fixture for CI that
has no cluster workload profile. This profile uses the actual browser-created
Gateway. It adds application evidence without new domain behavior in STEGO.
Common controller, transport, telemetry, event, and deployment mechanisms
remain generated by STEGO.

The [worker deployment check](worker-deployment.md) now covers separate
generated worker Deployments and telemetry across their replacement. Production
workload network isolation, certificate rotation, capacity, and full Sandbox
provisioning through the console remain open. This pass does not establish
production readiness.
