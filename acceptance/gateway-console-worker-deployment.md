# Gateway console deployment

The Gateway worker can render the console from the generated browser module.
The module is pinned to `e0ed90b1b46d`. It contains the qualified dashboard image
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
common state mechanism to use for the separate console session state. The console
state and its SQL schema are not yet connected.
