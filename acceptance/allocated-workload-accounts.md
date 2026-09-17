# Allocated workload accounts

The complete Gateway workflow passed at `0ec0180`. It pins the published common compiler at
`09efc7c7e588ecf9a2e3b4d7f3b536cac48b81eb`. The exact source passed the full compiler
suite and focused account checks. The signed immutable release and all four
assets were independently verified. CI generation passed and its checked output
is committed. The application checks and installation-policy review described below passed.

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

The change includes tests for missing, deleting, foreign, and denied accounts,
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

The following records close the application and installation checks for this
change. They do not close the full enterprise goal.


The first application check at `02e3ca8` failed before tests ran. Its root Go
module still selected an older Gateway-console package. The new dependency
selects the independently checked console module at `02e3ca8`. CI compares its
generated source and module files with the checked-in console before application
checks. Module resolution and image builds use the same explicit version.

The first policy render also stopped before rendering. Its endpoint fixture
assumed adjacent YAML fields. The corrected fixture selects the Gateway profile
and preserves its account declaration. Its 14 inspection tests and 12
installation tests passed locally. The replacement policy render passed in CI;
independent policy review and installation later passed as described below.
No cluster policy was changed during the failed render.


## Complete workflow and account checks

[Live run 35245846085](https://github.com/jsell-rh/hypershell-stego/actions/runs/35245846085)
passed all 11 required tests at `0ec0180`. The main workflow took 656.99 seconds.
Independent checks matched 1,441 source files, 416 generated hashes, the
published compiler package, and the deployed console image. REST and gRPC
access checks, filtered lists, denied writes, event delivery, PostgreSQL process
restart, namespace replacement, provisioner restart, and durable deletion passed.
Browser startup logs, traces, and metrics covered all eight required stages.
All three dashboard screenshots were reviewed. Independent cluster reads
confirmed that test resources were absent and the test lease was free.

The account record contains 24 ready-Pod observations across two Gateways,
three namespace instances, and six account objects. Four distinct account names
cover the owner and alias pairs. Names remained stable after namespace
replacement; account and Pod UIDs changed. The Gateway token mount was enabled,
the console token mount was disabled, and both account objects disabled automatic
token mounts. Actual authorization reviews denied worker account creation and
patch requests. The shared-cluster Sandbox guard remained active.

Hosted run `35241823877` passed 720 cases and 313 top-level tests at `51567de`,
with three generation drift checks and container cleanup. The image job built
seven images. UI checks passed 231 tests, and the hosted browser check passed.
Source comparison found only two CI reader files changed from `51567de` to
`0ec0180`; all application and generated files matched.

Independent installation checks verified 25 resource identities: 19 retained
objects and six new policy or binding objects. All six policies passed server
type checks. The restricted CI identity passed 12 named read checks and 12
denial checks. Installation and CI permission updates occurred after cleanup.
See the [combined evidence record](allocated-workload-accounts-evidence.json).

## Earlier failures and limits

The first full application run at `ad7cbb3` failed a namespace-count fixture.
The fixture omitted required account annotations. The corrected fixture uses
the common account-name function. A later image check at `96cbd4d` failed an
incomplete installation inventory mock; `51567de` corrected that mock. The
first live attempt at `51567de` stopped before creating a Job because the CI
identity could not read the new named policies. `0ec0180` corrected that read
permission. These attempts remain failed or cancelled records.

The PostgreSQL restart used the same fixture Pod. It does not prove RDS
failover. Separate Sandbox allocation and permissions, live Kata isolation,
backup and restore, cross-process fencing, and production capacity remain open.
The first capacity targets are 100 Gateways per instance, 100 service accounts
per Gateway, and healthy Gateway cleanup within 30 seconds. These are targets,
not hard limits. This workflow does not prove that capacity.


## Main repeat

At main source `110d9c4`, API run `35248320877` passed all 52 required tests.
Independent checks matched 1,443 source files, 416 generated hashes, and all
four published compiler package hashes. The completed API Job and its fixtures
were absent, and the shared lease was free at 16:53:26 UTC on 2026-09-17.

The hosted browser job in run `35248321021` passed in 75.71 seconds. Three
instances supplied all eight startup stages with matching logs, traces, and
metrics. Fixture and container cleanup passed. Both browser images were reviewed.
Seven generated image builds and 231 UI checks also passed. The core suite passed with 720 test cases, including 313 top-level tests,
and three checks found no generation drift. The public browser repeat failed before the network fault:
a console Pod was not ready after earlier API and Deployment observations.
Independent cleanup passed. The correction and its successful main repeat are recorded in
[the Pod readiness evidence](pod-readiness-evidence.md). See the
[main evidence record](allocated-workload-accounts-main-evidence.json).
