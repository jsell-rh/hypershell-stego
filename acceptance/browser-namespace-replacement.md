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
is still running. The complete core, ordinary browser, console, and service-image
jobs passed; full CI still fails on the unfinished CNPG and Sandbox checks.
The later SQL cleanup denial test is not part of this passing browser source.
