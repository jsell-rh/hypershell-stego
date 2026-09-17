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

Generation and application checks remain pending. Do not use this source as a
qualified deployment until those checks pass. Recreate controls planned
Deployment upgrades. It does not protect against manual scaling or fence an old
process on an unreachable node. Distributed writer fencing remains open.
