# Generated worker deployment acceptance

The browser Gateway workflow passed on 2026-09-12 UTC with STEGO
`5affc101795c43df2b4b8ee43c14209b5c68e2b7`. The test passed in 306.36 seconds.
Its race-enabled acceptance package passed in 307.409 seconds. Contract checks
passed in 1.060 seconds. The diagnostic privacy check also passed.

The fixed source is `/tmp/stego-worker-deployments-final-e9qiwj06/application`.
Results are in `/tmp/stego-service-results.zpGnDwPS`. The Job reached `Complete`
with exit zero. All 225 generated, state, and dependency hashes match both
generation passes, the post-test output, and the checkout. All changed test
and implementation source matches the fixed copy. The final console screenshot
was reviewed. All five test namespaces and the owned test, worker, and Gateway
cluster RBAC resources were removed. Their absence was verified.

## Application behavior

The API, console, account provisioner, database worker, identity worker, and
Gateway workload worker now run in six separate generated Deployments. STEGO
supplies their entry points, process bounds, health probes, image build files,
ServiceAccounts, Secret mounts, network policies, and deployment renderer.
The database and Gateway workers also use generated Kubernetes API token
projections and explicit RBAC rules. Hypershell declares the permissions and
supplies the domain controllers.

The console creates the actual Gateway. Generated workers provision its
PostgreSQL database, identity client, and OpenShell Deployment. Verified REST
and gRPC requests, filtered lists, denied callers, owner grants, and generated
event delivery pass. The Gateway retains provider data after Pod replacement.

The test then replaces all three worker Pods and requires different Pod UIDs.
The Gateway remains ready with current controller observations. Each of the
six worker process instances exports controller metrics and correlated logs
and traces. The test collector retains at most two instances per worker and
32 recent log or trace identifiers per instance. It consumes worker exports
without filling the browser checks' bounded channels.

API and console replacement, session key rotation, renewal, and confirmed
identity-provider sign-out also pass. The browser creates a service account
for this Gateway, then replaces the account provisioner Pod. The credential
obtains a real token and reads the stored provider from OpenShell. Account
reload, credential privacy, revoke, and delete checks pass.

## Identity and network access

The database and Gateway worker Pods mount the API token and CA at
`/var/run/stego-kubernetes`. The read-only projection has file mode 0440 and a
requested token lifetime of 3600 seconds. The identity worker has no Kubernetes
API token projection. Automatic ServiceAccount token mounting remains off for
all targets. Live Pod checks confirmed these restrictions.

Each worker has its own ServiceAccount. Six live RBAC checks passed. The
database worker can create Deployments and read ConfigMaps, but cannot read
Nodes. The identity worker cannot read Secrets through the Kubernetes API.
The Gateway worker can create TokenReviews, but cannot escalate roles.
The public records are `worker-permissions.json` and `worker-projection.json`
in the result directory. No token content was read for these checks.

The renderer receives exact Kubernetes API endpoint addresses and ports.
Worker ingress remains denied. Declared egress permits the application API,
telemetry collector, DNS, and the required provider endpoint. The identity
worker alone receives Keycloak admin credentials.

## Bounds and images

The root test namespace permits nine Pods, eleven CPUs, and 10 GiB of memory.
Each generated application container retains a one-CPU and 512 MiB limit.
The test container retains one CPU and 3 GiB; Chromium retains one CPU and
1536 MiB. The Job deadline is thirty minutes and the acceptance command has
ten minutes. The four owned Gateway and database namespaces retain their
[previous limits](browser-gateway-workload.md#test-profile).

The three new worker image digests are:

- Database: `sha256:3e8f3e63960ca504842f6af6aab1f243738d37bb1832fe5df8094142e9c042ce`
- Identity: `sha256:1c02a26e1cd853a11ae3ce5904c1147ccc345b3b3dce6d2eed4f4b78ff91f630`
- Gateway workload: `sha256:fa572f73aefd770683a29a08b8882fee7fe064ca9c2542c5a29cfe898ff7d741`

API, console, and account provisioner image digests match the
[prior deployment record](rpc-deployment.md). The images also match the first
worker attempt; its defects were in the permission declaration and test
collector. Use the [browser workload command](browser-gateway-workload.md#test-profile)
to repeat this check.

## Failed attempt and remaining scope

The first worker attempt failed because the database worker lacked ConfigMap
access. It received `403`, and the Gateway did not become ready. The test
collector also filled while provisioning waited. The correction adds only
ConfigMap access to the existing database rule and uses bounded worker signal
checks. The failed results are `/tmp/stego-service-results.pdoEQiNp`.

Full STEGO CI passed for the identity/RBAC change and its name-collision fix.
New full application CI remains a separate check.

These controllers still have declared cluster permissions for dynamic
namespaces. Production shared-cluster isolation requires a separate namespace
allocator and permissions limited to allocated namespaces. Kubernetes RBAC
cannot constrain a cluster rule by an owner label. Full Sandbox provisioning
through the console, workload network isolation, certificate rotation, and
capacity also remain open. This check does not run long enough to prove an
entire Kubernetes token rotation cycle. It does not establish production
readiness.
