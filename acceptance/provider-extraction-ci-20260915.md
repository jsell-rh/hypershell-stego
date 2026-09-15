# CI state during provider extraction

Application revision: `dec29c4424e04549eca5632899fcda7560d1da79`.
Compiler pin: `b0bd9a49722b0c2c8a0a1142a59e91c46bc2d6b6`.

The [Gateway API run 35027387195](https://github.com/jsell-rh/hypershell-stego/actions/runs/35027387195)
passed all 32 required checks. They include owner and event transactions,
filtered access, REST and gRPC behavior, generated CLI and SDK behavior,
restart, and SQL cleanup. The result has no failed or missing required test.
The Job, Pods, and private fixture resources were removed.

The [browser run 35027388882](https://github.com/jsell-rh/hypershell-stego/actions/runs/35027388882)
failed before it created a Job. The installed namespace-allocation policy
contained the CNPG database destination in `stego-cnpg-database-ci`. The external
database fixture expected a policy without that destination. The comparison
rejected this fixture mismatch. No application behavior was tested in this run.

The runner retained its Lease because the early failure prevented its allocation
preflight record. Operator inspection then verified the namespace identity and
confirmed that no test Job, Pod, Deployment, private fixture data, allocated
namespace, or allocated cluster-role binding remained. The operator released
the exact Lease with UID, resource-version, and holder checks. The installed
policy was retained. A later browser run needs the matching operator fixture;
do not remove the policy check or treat the failed run as a pass.

The CNPG job in [run 35027389182](https://github.com/jsell-rh/hypershell-stego/actions/runs/35027389182)
also stopped before setup. After queueing, its credential had less than the
required 45 minutes left. The credential check refused to start. The shared
Lease was free after this preflight failure. Renew the short-lived environment
credential before a new CNPG attempt; this failure is not a test result for
CNPG behavior.

Results, manifests, the policy difference, and the recovery record are stored in
`/home/jsell/.local/state/stego/runs/keycloak-provider-roles-20260915`.

These results do not prove adoption of the common Keycloak provider. The
application still uses its handwritten client. The
[provider boundary](https://github.com/jsell-rh/stego/blob/f5d35b9/specs/keycloak-provider-boundary.md)
defines the remaining adoption gate.

The core and browser acceptance jobs in run `35027389182` later completed
successfully. The service-image and web-console jobs also passed. The CNPG job
remains a credential preflight failure, and the Sandbox job remains deferred.
These results use the existing compiler pin and handwritten Keycloak client.
The final job metadata is stored at
`/home/jsell/.local/state/stego/runs/keycloak-provider-native-20260915/application-baseline-ci.json`.
