# Keycloak inventory during cleanup

The provider can remove a client after an inventory list and before the next
read of that client. The Gateway inventory must continue after this specific
not-found result. It must still read the other candidates and check their
current ownership. The provider page length controls the next page request.

A failed list, a denied read, a server error, or an invalid response still stops
the scan. The scan returns no partial result on these failures. It performs no
write. A short or empty list does not prove that cleanup is complete. Retained
account records and cleanup journals remain required.

STEGO already supplies the common bounded client, typed not-found result, and
cursor rules. This change uses that result in the Hypershell inventory view.
Gateway selection and ownership rules remain in Hypershell. The common
single-client read still returns not-found to its caller.

The new test covers both Gateway and global inventory. It removes candidates
at each position, removes a full page before a second page, and checks that
request errors and invalid current representations stop the scan. The hosted
provider check also runs the real late-creation recovery test. The original
recovery test is unchanged.

The failed full run is recorded in `keycloak-inventory-failure-evidence.json`.
Focused checks, full hosted checks, and the live application workflow must
pass on the corrected source before it can replace the current main branch.
