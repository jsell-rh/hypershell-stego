# Sandbox execution gate

On 2026-09-17, the user selected the current Hypershell and OpenShell setup.
The prototype no longer changes the workspace-copy helper's user or the shared
socket volume. Its mutation policy and beta API feature setup are removed. The
deferred test no longer requires those adjustments. Keep upstream Pod settings;
do not add a mutation service or maintain an OpenShell fork for these fields.

The reference grants the Sandbox account access to OpenShift's privileged SCC.
The variant must provide that declared account binding through STEGO's trusted
allocator, with separate namespace and permission controls. The existing
constructor guard remains active until that path is complete. Removing mutation
does not establish Sandbox support, VM isolation, or a passing current workload.
The credential-mount restriction for the workload and workspace-copy helper
remains in the prototype's rejection rules.

The [CI run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35266280159)
passed at `b5c536c`. Generated-source verification and all 28 required recovery
tests passed. Independent checks confirmed the source revision, test results,
removal of mutation code, and the retained constructor guard. Evidence is stored
under `~/.local/state/stego/runs/upstream-sandbox-setup-20260917/`.
This result covers compilation, generated output, and the selected recovery
tests. It does not cover live Sandbox or Kata execution.

On 2026-09-18, STEGO's common isolated-runtime allocation policy passed 31
server dry-run admission requests on jshell at compiler source `19cb3e2`.
These include the root workspace helper and ordinary socket volume, runtime
and account restrictions, host-access denials, and exact capability grants.
All 16 policies type-checked. The writer also lacked three tested cluster and
account permissions. Cleanup needed a separate UID-checked recovery; all 60
test resource paths were then absent and the test lease was released.
See the [STEGO result record](https://github.com/jsell-rh/stego/blob/e88c299/specs/isolated-allocation.md).

The [signed compiler release](https://github.com/jsell-rh/stego/releases/tag/compiler-1ab6aeaad1e9862386c1d8c3d67e124f6814b048)
is available. Its exact source passed the full compiler suite; the published
package passed independent download and signature checks. This compiler is now adopted here.
The [application checks](https://github.com/jsell-rh/hypershell-stego/actions/runs/35347816185)
passed at `bc03f5d`, with 313 top-level tests in the core suite and the rendered
browser workflow in its separate job. Provider discovery and journal recovery
also passed. Four tests that need separate database or live Kubernetes fixtures
were skipped. The Kata job remains deferred. See the
[adoption record](isolated-compiler-adoption-evidence.json).
The Sandbox candidate now declares its allocation profile, account binding,
Pod rules, and related network peers. Its workload client uses STEGO's namespace
and account checks. It no longer writes namespaces, accounts, or admission
policies. Cleanup waits for both Gateway and Sandbox namespaces to be absent,
including when the operator disables new Sandbox creation. The count controller
watches the allocated Sandbox namespace and preserves the public Gateway
namespace in API updates.

The [adapter check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35351125950)
passed at `bfb35eb`: 61 top-level tests in three packages with the race detector.
One SQL test was skipped because this check has no database fixture. Tests cover
positive and negative admission dry runs, changed setup rejection, namespace
cleanup, count updates, namespace UID replacement, and stale event rejection.
See the [adapter record](sandbox-adapter-evidence.json).

The branch now selects signed compiler `0fcdf3b`. Hosted regeneration passed at
`71663d9`. All 415 artifact files matched the applied output, and signature and
source checks passed. Commit `c6c185b` contains the generated Sandbox profile,
account binding, and related network rules. See the
[generation record](sandbox-generation-evidence.json).

The full application run at `c6c185b` passed. The core suite passed 319 tests
in 13 packages, including the required Gateway workflows. Four conditional
database or live Kubernetes tests were skipped. The live Kata job was skipped. The separate rendered
browser workflow passed in 97.91 seconds. Three console starts had matching
logs, metrics, and traces for all eight startup stages. Six provider checks and
28 journal recovery checks passed. The provider source and cleanup records
were verified. See the [application record](sandbox-application-evidence.json). The earlier adapter
checks used the prior generated output and cannot establish the complete
allocation path. Network-controller annotations and Sandbox network behavior
also need checks. Keep the constructor guard until
the complete path is verified. OpenShell container, image, credential-mount,
and placement rules remain application policy. Live Kata execution is deferred.

A further review found that the credential-mount rule did not block Secret
values supplied through `env.valueFrom.secretKeyRef` or `envFrom.secretRef`.
The Sandbox application rule now rejects both forms for all containers. The
setup probe also rejects these additions. Literal values, field references,
and ConfigMap references remain permitted. The pinned OpenShell driver uses
literal environment values and contains neither Secret reference form. Its
source hash was checked. This change does not modify OpenShell.

Hosted regeneration passed with signed compiler `0fcdf3b`; all 415 generated
files matched the verified artifact. The
[adapter check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35452128458)
passed at `7c4834e`: 62 top-level tests, including 40 cases that evaluate the
application expression and check its generated admission policy. One conditional
SQL test was skipped. The
[full application check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35452145938)
passed at `7c4834e`. The core suite passed 320 top-level tests in 13 packages,
including the required Gateway REST, gRPC, authorization, event, restart, and
durable deletion workflows. Four conditional database or live Kubernetes tests
were skipped, and the Kata job was skipped. Its rendered browser job passed,
with three console starts and matching logs, metrics, and traces for all eight
startup stages. The account
deletion screenshot was reviewed. No container memory or PID limit event occurred.
The [later adapter check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35452318045)
also passed at `9d0e7f8`. It repeated the 62 adapter tests and compiled the deferred
live test without starting its fixtures. That test now requires a denial from
the generated policy and includes Secret environment cases for the workload and
workspace helper. Live Kata execution remains deferred. The constructor guard
remains active. See the [credential rule record](sandbox-secret-environment-evidence.json).

The branch now selects [signed compiler `eed9066`](https://github.com/jsell-rh/stego/releases/tag/compiler-eed90662e4faa485032e3e2b0d056b6166f0d00b)
and its matching common registry and tooling. The Sandbox declaration selects
`pod_network_provider: openshift-ovn-node-identity`. STEGO generates Pod status
coverage and rules for OVN and Multus metadata changes by the assigned node.
Application configuration cannot grant these provider annotation domains.
Hosted generation passed at `a0ddfae`; all 415 files matched the checked artifact.
Commit `be5d674` contains the generated output. Application handlers and module
dependencies are unchanged from the Secret environment application check.
The [adapter check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35453493088)
passed against the new output: 62 top-level tests, including 40 Secret environment
cases. One conditional database test was skipped. The deferred live test also
compiled without starting its fixtures. The complete application allocation and
network gate still needs a separate check. The constructor guard
remains active. See the [network metadata record](sandbox-network-metadata-evidence.json).

The user deferred the live Kata Sandbox test on 2026-09-15 because no suitable
cluster is available. CI marks this job as skipped. It is not a passing isolation
test. The ordinary code, protocol, authorization, and count-controller checks
remain active. Resume this live gate when a cluster with a verified Kata runtime
and a restricted test identity is available.

VM isolation, hostile-workload behavior, and runtime capacity remain unverified
on the current deployment. The earlier results below apply only to their
recorded fixture and source. They do not establish current OpenShift support.

## Historical fixture, now withdrawn

This gate extends the Gateway application workflow with sandbox creation and
command execution. It uses the same generated API, database, identity, events,
and controller clients. Sandbox placement and OpenShell policy stay in the
Hypershell layer. This change does not add a Hypershell rule to STEGO.

The earlier gate used Linux amd64, Docker, usable KVM, Python 3, and `zstd`.
The following command is historical. Do not run the withdrawn kind fixture:

```sh
export STEGO_TEST_POSTGRES_DSN='postgres://...'
scripts/check-sandbox-workload.sh
```

The test creates a disposable kind cluster. It installs Kata 4.1.0 in that node,
with the QEMU runtime and a guest kernel. It does not install a host runtime.
It checks the archive SHA-256 before extraction. Set `STEGO_TEST_KATA_ARCHIVE`
to a cached copy of the same archive to avoid a repeated download. The checksum
check still applies. The archive is about 970 MB; leave space for extraction,
container images, and test data.

The fixture also pins kind 0.33.0, Kubernetes 1.35.8, cert-manager 1.21.1, and
Agent Sandbox 0.5.4. The Gateway and supervisor use the reference OpenShell
release, `v0.0.109-rhaiv.0`, with image digests. The sandbox image is also pinned.
The fixture expands shared memory inside the kind node to 8 GiB before it starts
a VM. Docker's default 64 MiB caused QEMU to fail in the initial probe. The node also
sets a host Pod process limit. The Kata OCI base specification sets a hard
`RLIMIT_NPROC` of 512. This limit applies to processes and threads with the same
non-root user ID inside the VM. It does not limit the trusted root helpers.
The test tries to raise this limit and to fork past it. The kubelet setting and
both OCI cgroup process-limit fields did not set a guest limit in the probes.
The pinned runtime removes the dedicated PID field. Its agent protocol does not
carry the cgroup v2 `unified` map. See the containerd
[base specification setting](https://github.com/containerd/containerd/blob/v2.2.0/docs/cri/config.md).

An operator can select a previously installed runtime with
`HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS`. This option is experimental. The
operator must verify the runtime's isolation and node configuration. A runtime
class name does not prove a security boundary. An empty option preserves the
restricted namespace behavior of the provider-management gate.

With this option, the controller places sandboxes in a separate namespace.
The public Gateway namespace and its restricted Pod policy do not change.
The sandbox namespace gets its own service account and Gateway access role.
The pinned sidecar mode separates the workload from the network supervisor.
Process and binary checks remain enabled in the network policy. The workload
and workspace setup must run as a non-root user and drop all capabilities. It cannot mount the
client Secret or bootstrap token. Only pinned network helpers can request their
required extra capabilities.
Only the verified sandbox client certificate is copied there. Database passwords,
Gateway signing keys, and encryption keys stay outside that namespace.

Admission requires the selected runtime. It rejects host namespaces, host ports,
host storage, privileged containers, extra capabilities, device requests,
arbitrary annotations, and access to other sandbox storage. Allowed storage is
limited to the sandbox's own workspace claim, temporary storage, the pinned
supervisor image, its client certificate, and its audience-bound bootstrap token.
A mutation policy puts the sidecar socket in a memory volume and changes
workspace setup to the workload user. The pinned driver has no setting for
these fields. Disk storage caused a refused Unix socket connection in Kata.
The policy changes only these fields, in the exact sandbox namespace. It uses
the Kubernetes 1.35 beta mutation API. The cluster must enable
`MutatingAdmissionPolicy` and `admissionregistration.k8s.io/v1beta1`. The test
fixture enables both. See the [Kubernetes 1.35 policy documentation](https://v1-35.docs.kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/).
A missing API prevents the controller from enabling sandbox capabilities.

The controller sends positive and negative dry-run requests before it enables
sandbox capabilities. The positive check also verifies the memory volume. A failed check leaves a
new namespace restricted. Cleanup
keeps admission active until that namespace is absent.

The test requires a complete create, read, execute, and delete path. It requires
Landlock in `hard_requirement` mode. An ungranted user must not create or execute
a sandbox. The owner command must run as a non-root process under a separate
kernel, and it must not read the sandbox client key. Sandbox execution and a stored file
must survive Gateway and database restart, including Gateway namespace replacement.
Tests also submit Pod changes
that try to bypass admission.

Production runtime selection remains open. The user has been asked whether
production clusters can require a VM runtime. The fixture is compatibility
evidence, not a production support claim. Network isolation outside the VM,
resource quotas, image vulnerability review, hostile-image tests, device support,
OpenShift admission, and runtime capacity still need separate evidence. The
standard kind network does not enforce Kubernetes NetworkPolicy objects.

Set `STEGO_TEST_KEEP_CLUSTER=1` only for local fault analysis. The script then
retains its test cluster and prints the cluster name and scratch path. Remove
that cluster with the printed kind binary when the analysis is complete.
The default removes the cluster and its scratch files.

The memory volume requests 16 MiB. The pinned Kata memory-volume path does not
carry this size limit into the guest mount. Guest memory limits still apply.
The stock Kata configuration disables guest seccomp from the OCI specification;
this gate does not prove a runtime-default seccomp profile. The supervisor's
Landlock requirement remains active. These limits need review before production.

The full local gate passed on 2026-09-09 from a fresh cluster with no manual
cluster changes. The Gateway and sandbox test took 292.75 seconds. The recovery
and workload package took 350.790 seconds with the race detector. Sandbox
startup took 65.32 seconds with an empty image cache. A prior full run on a warm
node took 254.27 seconds, with sandbox startup in 11.07 seconds. In both runs,
the process-limit probe reached 504 children before the kernel denied a fork.
These results are acceptance evidence, not a capacity benchmark.
