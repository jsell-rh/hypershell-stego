# Gateway console deployment

The Gateway worker can render the console from the generated browser module.
The module is pinned to `554adff159dbe`. It contains the qualified dashboard image
and the STEGO deployment renderer. The worker does not keep a second copy of
those Pod and Service definitions.

`Kubernetes.EnsureConsole` requires the assigned Gateway namespace and its
allocator-owned network policy. It reads four controller-owned Secrets. Each
Secret must have the expected name, namespace, type, owner, UID, and version.
STEGO checks the Secret identities and bounds their data through
`OpaqueSecretSetDigest`. The worker rejects a Secret that is being deleted or has invalid data. A hash of
all four data maps causes a new rollout after a credential change. Metadata-only
changes do not cause a rollout.

The worker applies only the generated ServiceAccount, Deployment, and Service.
The allocator keeps control of network policy. Console Pods do not carry the
Gateway ID label: that label would make the Gateway Service send requests to
console Pods. Readiness requires the owned Deployment to complete its rollout.

## Current test scope

The local tests check the generated resource set, Service selector separation,
configuration changes, invalid dependencies, and allocation refusal. They do
not prove a live console deployment.

This entry point is not yet called by the Gateway reconciliation loop. The next
application test must add these parts before it can use the entry point:

- Durable, separate browser session database credentials and encryption keys.
- The generated browser schema, with a checked migration path.
- Runtime and application Secrets with separate contents and access.
- Allocator quota, ServiceAccount bindings, and approved console network peers.
- Public TLS, login, logout, dashboard requests, restart, and telemetry delivery.

Keep reusable state and migration mechanisms in STEGO. Keep Gateway placement,
client policy, and component selection in Hypershell.

The Gateway now uses STEGO's `LoadSecretState` for its immutable key Secret,
public digest marker, namespace pin, and retained registration. The common
runtime calculates the digest. Hypershell supplies key policy, the SQL absence
check, and the authorized registration callbacks. The prior data hash is retained
in a test fixture to check recovery of the existing storage format. This is the
common state mechanism to use for the separate console session state. The console entry point now uses this mechanism for its separate database and
session key.


## Component state and cleanup

The console uses separate internal SQL state methods. An older API that lacks
those methods returns `Unimplemented`; it cannot interpret a console write as a
Gateway write. Responses identify the component as well as the Gateway and
cluster. The public Gateway API still has no database ID field.

Both components use STEGO's retained effect bindings. Gateway state keeps its
existing scope. Console state has a separate scope and digest. The exact cluster
write and cleanup grants apply to both. Registration requires the current live
Gateway revision. Deletion prevents new registration.

The console worker must close registration before database deletion, then call
`CompleteGatewayConsoleSQLCleanup` only after database removal succeeds. STEGO
stores a separate immutable completion record in the same transaction. The API
rejects aggregate SQL cleanup while registered console state lacks that record.
An older worker therefore cannot complete deletion while console cleanup is
unfinished. The Gateway worker now closes console registration, removes its logical database,
and confirms completion. It attempts Gateway cleanup even if console cleanup
fails. Each database has an eight-second work context.

The expanded API test checks separate records, denied callers, two API restarts,
registration closure, and completion. It supplies the cleanup observation itself;
it does not claim that a console database was removed. The live console workflow
must prove that the worker sends this observation only after actual removal.

## Generated schema setup

The console module at `554adff` now contains the generated `out/browser/schema`
package. STEGO owns its DDL, bounded transaction, retry checks, and runtime grant
checks. Schema setup requires a separate database owner connection. The browser
login has data access only. The generated session store checks the schema before
startup and uses qualified table names.

The module build and repeat generation passed in run `35127006689`. All 85 source
files matched the committed module. See [the module record](console-schema-module-evidence.json).
This check does not run the schema against PostgreSQL. That compiler check is
pending. The module uses compiler `f6bf115`; a later compiler change also preserves
cancellation and deadlines. Adopt the qualified final compiler before deployment.

The root worker imports module `554adff159dbe`. `EnsureConsole` now calls the
common owner connection with `ManagedSchema: true` and the generated schema
setup. It checks the runtime database URL and session key against retained state
before deployment. The upstream application Secrets cannot use those file keys.
The namespace allocator creates a separate state profile when the Gateway has a
console address. It retains that namespace until SQL and workload cleanup finish.

The extended real SQL test provisions Gateway and console databases, tests denied
runtime DDL and cross-database access, restarts the adapter, checks retained
session rows and keys, and removes the console database while Gateway cleanup
cannot read its keys. Its new result is pending. The reconciliation loop still
needs the complete dependency Secret, TLS, network, and deployment path.
