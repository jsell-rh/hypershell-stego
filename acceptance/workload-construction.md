# Common Gateway workload construction

The Gateway worker uses the generated STEGO `workload` component to build its
Deployment and Service. Hypershell selects the OpenShell image, command,
configuration, ports, health endpoints, resource budget, allocated account, and
verified dependencies. It selects
[compiler 2cb6bdac](https://github.com/jsell-rh/stego/releases/tag/compiler-2cb6bdac38c5bddc8d5e47379536429c13bc8604).
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

The compiler passed 83 focused cases and all six full-check jobs for the selected
source. The focused cases include serialization through Kubernetes Deployment
and Service types. The signed release files match the tested compiler.
[Regeneration run 35528699463](https://github.com/jsell-rh/hypershell-stego/actions/runs/35528699463)
produced 427 files. The workload library matches the compiler template and
common generated header. The CLI compiler identity changed to the selected
revision, and the startup constructor index accounts for the added component.
Other generated code and module files did not change. The Gateway console
module therefore keeps its existing source pin. See the
[generation evidence](workload-construction-generation-evidence.json).
Application checks and the live adoption workflow remain pending.
