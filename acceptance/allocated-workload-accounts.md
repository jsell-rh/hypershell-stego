# Allocated workload accounts

This branch is under qualification. It pins the published common compiler at
`09efc7c7e588ecf9a2e3b4d7f3b536cac48b81eb`. The exact source passed the full compiler
suite and focused account checks. The signed immutable release and all four
assets were independently verified. CI generation passed and its checked output
is committed. Do not deploy this branch before the application checks and
installation-policy review.

The Gateway allocation profile declares two account aliases: `gateway` and
`console`. STEGO owns their names, creation, owner checks, token defaults,
permission bindings, and admission rules. Hypershell selects the alias for
each workload. It keeps the upstream Gateway and dashboard application policy.

The worker checks account readiness before it reads or writes workload
dependencies. It also checks namespace network isolation before it writes.
Missing or deleting accounts keep reconciliation pending. Invalid ownership,
settings, or failed reads stop the operation. The worker has read-only account
access. It no longer creates or patches accounts.

The console uses the generated deployment renderer with an existing account.
The Gateway explicitly enables its Pod token mount. The console disables that
mount. Both account objects disable automatic token mounts. The unused Sandbox
account is removed from the Gateway resource list. The shared-cluster Sandbox
guard remains active; this change does not qualify Sandbox execution.

The draft includes tests for missing, deleting, foreign, and denied accounts,
changed token settings, and missing namespace annotations. The public workflow
checks distinct accounts and Pod token settings whenever each Gateway becomes
ready, including after worker restart and namespace replacement. It records
namespace, account, and deployment UIDs without credentials. The network probe
also uses the checked generated account name. Authorization checks require
worker account creation and patch to be denied.

Regeneration run `35237067181` passed at `c1f2875`. Independent checks matched
all 415 generated files and all three drift checks. The three deployment
renderer copies match the common renderer that passed its focused tests. The
allocation configuration changes only the two account aliases and worker
account permissions. See the [generation record](allocated-accounts-generation-evidence.json).

Hosted application checks, installation-policy review,
and the complete bounded cluster workflow remain required.
Do not change cluster policy while the current main browser test is active.


The first application check at `02e3ca8` failed before tests ran. Its root Go
module still selected an older Gateway-console package. The new dependency
selects the independently checked console module at `02e3ca8`. CI compares its
generated source and module files with the checked-in console before application
checks. Module resolution and image builds use the same explicit version.

The first policy render also stopped before rendering. Its endpoint fixture
assumed adjacent YAML fields. The corrected fixture selects the Gateway profile
and preserves its account declaration. Its 14 inspection tests and 12
installation tests passed locally. The replacement policy render passed in CI;
independent policy review is still pending. No cluster policy was changed.
