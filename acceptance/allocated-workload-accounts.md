# Allocated workload accounts

This branch is a draft. It requires the common account readiness API from
STEGO source `09efc7c7e588ecf9a2e3b4d7f3b536cac48b81eb`. That source is under
qualification. The compiler pin and generated output still use the previous
package. Do not deploy this branch before compiler publication, regeneration,
and the application checks.

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

Only formatting and source checks have run for this draft. Required next steps
are the signed compiler release, regeneration in CI, hosted application checks,
a reviewed installation-policy update, and the complete bounded cluster workflow.
Do not change cluster policy while the current main browser test is active.
