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

The new check passed formatting, source checks, and the complete acceptance
package build in the ordinary browser CI job. The live recovery workflow is
now running. No namespace recovery result is claimed until that workflow passes.

The pushed candidate is `48eae25`. Its first push runs were cancelled before
a Job started. The replacement [browser check](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937103556)
and [API check](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937105018)
use that same commit. No recovery result is available yet.
