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
repeat above uses the committed generated source.

The journal repeat at `4897fc1` passed in
[run 35216258643](https://github.com/jsell-rh/hypershell-stego/actions/runs/35216258643).
Independent checks found each of the 28 required tests exactly once, with no
failed or skipped test. Generated-source checks and hosted service cleanup also
passed. These tests cover stored journals, provider failures, access rules,
concurrent registration, event rollback, and recovery from saved checkpoints.
They do not prove whole-database restore or distributed writer fencing. See the
[journal record](provisioner-rollout-journal-evidence.json). The full application
suite failed later; see the result below.

## Provider job verification

The earlier provider run at `bf7b8da` passed its 61-client cleanup test, including
20 stored journals at restart and preservation of the unrelated client. That
job copied the compiler pin but did not check generation. Its result proves the
committed runtime at that source; it does not prove compiler adoption. See the
[scoped record](provisioner-rollout-prior-provider-evidence.json).

The provider job now checks committed generation with the pinned compiler before
it builds or starts the provider test. The generation step alone receives the
GitHub read token. Compiler and registry input changes also trigger this job.
The new result remains pending.

## CNPG application result

[Run 35217578495](https://github.com/jsell-rh/hypershell-stego/actions/runs/35217578495)
passed all 11 required tests at `b65439f`. The complete browser Gateway workflow
took 711.81 seconds. Independent checks matched 1,416 source files and 415
generated hashes before and after the tests. The compiler package and Gateway
console image matched their verified records. All three screenshots were
reviewed: the workspace, invalid JSON feedback, and editor selection were
visible. No global policy was submitted.

The workflow covered REST and gRPC access, filtered lists, denied requests,
event delivery, login and logout, provider outage, and process restart. Gateway
Pod replacement and namespace replacement retained provider data and SQL
identities. CNPG primary replacement took 67.18 seconds and retained database
and role identities, credentials, provider data, and installation data. Account
cleanup and Gateway deletion passed. Worker logs, metrics, and traces passed
the correlation checks.

Both live provisioner startup checks confirmed one replica and `Recreate`.
The final object collection was empty after deletion, so the independent check
uses the verified generated template and those two live API assertions. The
test Pod scheduled without delay or a manual placement change. Independent
cleanup found no test runtime, private fixtures, allocations, or either test
volume. The shared test Lease was empty. See the
[CNPG evidence](provisioner-rollout-cnpg-evidence.json).

This pass does not explain the earlier main CNPG RPC timeout. It does not
qualify the later database credential API, distributed writer fencing, or live
Kata isolation. The latter test remains deferred.

## Failed hosted application result

[Run 35216177325](https://github.com/jsell-rh/hypershell-stego/actions/runs/35216177325)
failed at `4897fc1`. The core job had 306 passing top-level tests and two failures:
the final cleanup recovery event exceeded its read deadline, and the unrelated
Keycloak client response comparison failed. Hosted service cleanup passed.
The deadline case passed in the later two-case repeat; its earlier failure
remains unexplained. The client comparison now follows the provider's scope-set
contract. Its repeat remains pending. The CNPG pass does not replace these
failed results, and application promotion remains pending.
