# Provisioner rollout policy

The provisioner changes external identity-provider state. Its lifecycle gates
are local to one process. The generated RPC Deployment previously used
`RollingUpdate`, which permits an extra Pod during an upgrade. That conflicts
with the current requirement to run one provisioner writer.

Hypershell selects `rollout_strategy: Recreate` through the common STEGO
Kubernetes component. The compiler and all common registries select source
`4fc880bcec6bc2555aca6b5c86bb8b893eaf8254`. No local component copy or handwritten
Deployment is added. The common renderer still supplies permissions, network
rules, resource limits, probes, and container settings.

The public and CNPG Gateway tests inspect the actual provisioner Deployment
after initial startup and after restart. They require one desired, updated,
ready, and available replica, the observed revision, and `Recreate` without
rolling-update fields. Existing outage tests require denied new account writes
and recovery with stable SQL and credential identities.

Generation passed in CI and was checked independently. Application checks
remain pending. Do not use this source as a qualified deployment until those
checks pass. Recreate controls planned
Deployment upgrades. It does not protect against manual scaling or fence an old
process on an unreachable node. Distributed writer fencing remains open.

## Generation evidence

[Run 35215892388](https://github.com/jsell-rh/hypershell-stego/actions/runs/35215892388)
regenerated all three modules at source `bf7b8da`. Independent checks matched
all 414 archived files to the applied patch, the exact source archive, the
published compiler verification record, and all three drift checks. Only the
three generation-state files, the CLI compiler identity, and the provisioner
Deployment template changed. Parsed old and new manifests differ only in that
Deployment's strategy. Permissions, network rules, probes, limits, and other
resources are identical. See the [generation record](provisioner-rollout-generation-evidence.json).

The generated Gateway console check passed at `4897fc1` in
[run 35216103989](https://github.com/jsell-rh/hypershell-stego/actions/runs/35216103989).
Independent checks matched 129 source files, repeated generation, dependencies,
the image executable, and the pulled image digest. The installer record matches
the published compiler. The executable is identical to the earlier qualified
Gateway console. See the [module record](provisioner-rollout-module-evidence.json).

The first module and journal jobs at input-only source `bf7b8da` failed their
committed-generation checks before the generated files were applied. Their logs
show the generation-state differences. Those runs are not passes. The module
repeat above uses the committed generated source; the journal repeat and full
application suite remain in progress.
