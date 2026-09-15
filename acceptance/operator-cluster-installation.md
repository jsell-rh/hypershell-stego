The operator now installs the browser fixture's generated cluster resources.
The test Pod applies only the generated namespace scope. This change uses
STEGO's common deployment renderer; it does not add a Hypershell rule to STEGO.

The installer compiles only the standard-library renderer on the host, with one
build process and a 256 MiB Go memory target. Application builds and workload
tests remain in the bounded cluster Job. The installer reads the frozen source,
renders the selected targets, checks their kinds and namespace prefixes, and
checks that every resource name is absent before the first write. It does not
accept manifests from the test Pod. Admission policy type checking must finish
without warnings before the test starts.

Cluster roles and policies have no image or network endpoint. Installation uses
fixed valid placeholders for those renderer inputs. Before each deployment,
the Pod renders the cluster scope with its actual arguments and requires an
exact match with the operator's retained manifest. A mismatch stops deployment.
The Pod then renders and applies the namespace scope. The operator credential
never enters the Pod.

The fixture's test identity no longer has RBAC write, bind, or admission policy
write permissions. Requests for the three worker tokens are limited to the
control namespace. Live checks use the actual Pod credential. They check denied
installation rights, named token request scope, and a denied cluster-role
creation request with server dry-run.

The installer records resource UIDs. Cleanup reads each resource, checks its
recorded UID, and sends DELETE with that UID as a precondition. A replaced
resource stops cleanup and keeps the shared Lease for inspection. The CLI sends
the DeleteOptions body through its
[raw DELETE path](https://github.com/kubernetes/kubectl/blob/v0.35.0/pkg/cmd/delete/delete.go#L297).
Normal REST Gateway deletion must still finish before fixture cleanup. The
outer operator process owns fallback cleanup after a test failure.

The result below used broad namespace and Secret access for workload inspection.
The current fixture removes those rights and uses
[declared namespace inspection](browser-inspection.md). Its complete live check
passed in 342.91 seconds. The Gateway, CNPG, and Sandbox CI checks remain open and must
not be reported as passed until their installation fixtures work with the
restricted identity.

The complete Kubernetes browser workflow passed in 330.01 seconds. All 17 live
installation-access checks passed. The six deployments matched the operator's
cluster manifests before startup and after replacement. The workflow also
passed Gateway creation, grants, REST and gRPC access, events, SQL isolation,
active-session quarantine, provider data recovery, worker telemetry, browser
credentials, logout, and normal Gateway deletion. The supplied PostgreSQL
server and installation data remained available after Gateway cleanup.

The complete acceptance package compiled; no source file was excluded. Repeated
generation and the check after tests matched all 229 recorded files. The source
matched the frozen copy. The Job completed, and the operator removed the test
namespace and owned resources before it released the shared Lease. See
[the source and runtime evidence](operator-cluster-installation-evidence.json).
Seven small installer tests also passed. They check scope validation, existing
resources, read failure, UID preconditions, and preservation of replaced objects.
