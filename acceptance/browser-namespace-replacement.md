The browser workflow now includes loss of a Gateway's workload namespace while
all three controllers stay running. The test submits one deletion with the
observed namespace UID. It does not repeat that deletion after a replacement
can exist. The Gateway record, retained-state namespace, and supplied PostgreSQL
server remain in place.

Recovery must produce a new namespace and Deployment UID. The retained-state
namespace and source Secret must keep their UIDs, fingerprint, and Secret data.
Published credential and key Secrets must have new UIDs and the original values.
The SQL database, login role, and owner role must keep their PostgreSQL object
IDs. Verified RPC must recover the provider record, and an ungranted caller
must still be denied. The other Gateway must remain healthy and retain its SQL
objects and credentials. All three controller Pods must retain their UIDs with
no container restart. Namespace permission and admission checks run again after
recovery.

The test uses the generated Kubernetes client and the declared allocator token.
It adds no production permission. Evidence records public UIDs and comparison
results. It does not record credentials, Secret contents, or hashes of secrets.

The [live browser workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937103556)
passed at `48eae25`. The complete workflow took 372.73 seconds. Namespace recovery
took 43.73 seconds in this fixture. All three controller Pods retained their
UIDs without container restarts. The new namespace and Deployment had different
UIDs. Retained source data, SQL object IDs, credentials, provider data, and access
rules passed their checks. The other Gateway retained its SQL state and was
ready after recovery. This does not prove uninterrupted availability.

All 870 source files match the pushed commit. All 229 generated files match
the frozen source, repeated generation, and generation after the test. The 16 CI
probes, 57 application access checks, and six admission probes passed. Host
cleanup removed test data and allocations and preserved the operator installation.
See the [verified result](browser-namespace-replacement-evidence.json).

The first push runs were cancelled before a Job started. The passing browser
run is the replacement dispatch on the same pushed commit. Its separate
[API check](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937105018)
passed all 30 required tests. Its 870 source files and 228 generated files
match the commit; its Job, Pods, and fixtures are absent. The complete core,
ordinary browser, console, and service-image
jobs passed; full CI still fails on the unfinished CNPG and Sandbox checks.
The later SQL cleanup denial test is not part of this passing browser source.

The current test also gives a viewer a Hypershell grant and a separate default
workspace membership before namespace deletion. Both must survive replacement.
Before and after recovery, the viewer must see only its granted Gateway and
workspace. Provider reads must redact credentials. Provider writes, workspace
creation, membership changes, administrative RPC, and Gateway writes must fail.
An owner-only workspace must remain inaccessible.

After recovery, the test removes workspace membership and requires the same
issued token to lose workspace access. It restores membership, removes the
Hypershell grant, and checks API denial and new-token denial. Finally, workspace
removal must also deny the earlier token. These requests pass through the
generated browser backend and the actual Gateway RPC server. The test needs no
additional Kubernetes permissions. The earlier viewer helper had no caller;
its presence did not establish this behavior in the current workflow.

The complete acceptance package compiled with these checks. The earlier
results above did not cover the added viewer checks.

The [viewer-source API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34950955027)
passed all 30 required tests at `5dc5742`. Verification matched 886 source files,
229 generated files, all generation records, and cleanup. This API run compiles
the viewer helper but does not execute it against a live Gateway. Browser run
`34950955188` was cancelled while pending and requeued on the same commit after
credential rotation. Its second attempt was later cancelled while still pending.
The newer account-source browser run `34951393842` includes identical viewer
and namespace recovery checks, plus live account deletion. That complete run
passed the required live gate. No pass is claimed for either cancelled
attempt. See
[the verified evidence](browser-viewer-recovery-evidence.json).

The complete supplied PostgreSQL browser run `34951393842` passed at `10a0827`
in 395.99 seconds. Namespace recovery took 45.86 seconds. Viewer membership,
filtered lists, and denied writes passed before and after replacement. Both
access-removal paths passed after recovery. The same three controller Pods
remained running. SQL object
identities, credentials, encryption keys, and provider data remained unchanged.
The other Gateway remained available. All 887 source files, 230 generated
files, access and admission checks, generation records, and automated cleanup
passed verification. No public Gateway connection or Sandbox execution is
included in this result.
