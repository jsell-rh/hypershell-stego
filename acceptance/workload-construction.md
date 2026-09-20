# Common Gateway workload construction

The Gateway worker uses the generated STEGO `workload` component to build its
Deployment and Service. Hypershell selects the OpenShell image, command,
configuration, ports, health endpoints, resource budget, allocated account, and
verified dependencies. It selects
[compiler ad71bc71](https://github.com/jsell-rh/stego/releases/tag/compiler-ad71bc71aa5081c3376fca9dc701b2eff5f4cc4e).
STEGO supplies the Kubernetes objects, fixed security
settings, Secret file modes, mount rules, validation, and configuration digest.

Gateway ownership, placement, release selection, OpenShell configuration,
certificate issuer selection, and route verification remain in Hypershell.
The worker still requires namespace allocation and dependency verification
before construction. The builder does not create accounts or grants.

The Gateway keeps one replica with the Recreate strategy. Its CPU request is
100m and its memory request is 256 MiB. Limits remain 500m and 512 MiB. Temporary
storage, ports, startup checks, readiness checks, and liveness checks keep their
previous values. The Gateway explicitly requests the allocated Kubernetes API
identity. Sandbox construction is unchanged.

The Pod uses the common `stego.dev/config-sha256` annotation. This change causes
one rollout when an existing Gateway first uses the new declaration. Later
rollouts use the complete verified ConfigMap and Secret contents. API metadata
alone does not change the digest. Secret bytes do not enter a Deployment,
Service, or error message. Public TLS verification returns the same Secret
object that supplied the verified certificate; construction does not read it
again.

Construction errors stop Deployment and Service writes. The focused checks
cover the application contract, dependency changes, metadata changes, and
rejection without partial output. STEGO tests the common security and input
rules. Full application checks, regeneration checks, and the complete live
Gateway workflow are required before this adoption is accepted.

This change does not establish the production capacity target. It does not
change the deferred Kata test or the use of the upstream Sandbox controller.

The selected compiler passed 114 focused cases and all six full-check jobs.
The focused cases include Kubernetes API serialization and updates through the
generated Kubernetes client. The update checks cover removed arguments,
environment values, mounts, volumes, and image pull Secrets. They also cover
strategy changes and repair of changed security settings. Fields owned by the
workload profile use explicit removal values. Other Kubernetes defaults remain.
The signed release files match the tested compiler.

[Regeneration run 35533040709](https://github.com/jsell-rh/hypershell-stego/actions/runs/35533040709)
produced 427 files from seed `84c7d6bc`. The workload library matches the selected
compiler template and common generated header. Only the workload library, CLI
compiler identity, and three generation state files changed. The startup
constructor index, other generated code, and module files did not change. The
Gateway console module keeps its existing source pin. See the
[generation evidence](workload-image-reference-generation-evidence.json).

The earlier adoption at `2b58aa97` passed its complete service Deployment
workflow on jshell. The check matched signed API and worker images before and
after Pod replacement, matched 428 generated file hashes, and verified cleanup.
That result used compiler `2cb6bdac`. It does not establish acceptance of the
corrected generated source. Application checks and the complete live workflow
remain required for this source before main promotion.

The earlier browser workflow in run
[35531135902](https://github.com/jsell-rh/hypershell-stego/actions/runs/35531135902)
failed before a Gateway Pod was created. The common validator rejected the
OpenShell image because it had a tag before its SHA-256 digest. The compiler
regression test reproduced the rejection. The selected compiler accepts that
form and still requires the digest. It also rejects invalid tags, missing
digests, and invalid digest values. No image value is changed by construction.

The failed browser run is not an acceptance result. Independent cleanup found
no test workloads, fixtures, allocated namespaces, or CI Jobs. The shared test
Lease was free, and all 32 standing test resources were unchanged. The corrected
source must pass the complete application workflow before main promotion.

The corrected application source `caffd04` passed its full hosted check in
[run 35533192186](https://github.com/jsell-rh/hypershell-stego/actions/runs/35533192186).
It retained all 1,162 core cases across 363 top-level tests. The rendered browser,
UI, and service-image jobs passed. The five existing core skips remain recorded;
the live tests supply separate evidence. The focused construction and adapter
checks passed. All seven application images passed independent source, binary,
image, registry, and signature checks.

The service Deployment workflow then passed on jshell in 120.13 seconds. It
checked HTTPS and gRPC, atomic owner grants, rollback, filtered access, event
delivery, Keycloak reconciliation, and API and worker Pod replacement. The
independent checks matched all seven signed images, both compiler binaries,
and 428 generated file hashes. Two ready API Pods and two ready identity-worker
Pods had the expected image identities and no observed restarts. The runtime
supplied logs, metrics, and traces. Cleanup removed the dedicated test namespace,
released the shared Lease, and left all 32 standing resources unchanged. See the
[service evidence](workload-construction-service-evidence.json).

The complete browser workflow then passed in
[run 35535544885](https://github.com/jsell-rh/hypershell-stego/actions/runs/35535544885).
All 11 required tests passed; the main workflow took 541.55 seconds. Independent
review matched 1,647 source files and 428 generated file hashes. The workflow
covered real Keycloak login, Gateway creation, grants, REST and gRPC access,
event delivery, browser sessions, and the upstream dashboard. It recovered from
namespace replacement, worker and provisioner restarts, and a PostgreSQL process
restart. It also checked denied cleanup and recovery. The paused allocator test
kept a deleting Gateway visible through REST and gRPC until its state namespaces
were removed. Correlated logs, metrics, and traces passed their checks.

Two admitted Gateway Pods were ready with the exact declared image tag and
digest. Both had non-root execution, a read-only root filesystem, no privilege
escalation, all capabilities dropped, default seccomp, read-only configuration
mounts, and the declared probes and resource limits. The object ownership chain
and configuration digest were checked. Four retained screenshots had no observed
layout defects. This is not a full accessibility review.

One normal deletion started with 100 live service accounts. All accounts and
provider identities were removed, and all cleanup journals and audits were
complete. The complete result was observed after 32.565 seconds. Allocation and
Gateway finalization were still pending at the 30.36-second observation. The
30-second target remains open. This small workflow does not prove the capacity
target of 100 Gateways per instance.

Independent cleanup found no test workloads, fixtures, allocated namespaces, or
CI Jobs. The shared Lease was free, and all 32 standing resources were unchanged.
See the [browser evidence](workload-construction-browser-evidence.json). Together
with the service and hosted checks, this result accepts the common workload
construction adoption. Live Kata isolation, OpenShell Sandbox execution,
production capacity, and the remaining enterprise requirements remain open.
