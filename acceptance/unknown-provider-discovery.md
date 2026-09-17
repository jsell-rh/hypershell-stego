# Unknown provider client cleanup

This check creates 61 real Keycloak clients for a Gateway without local account
rows or cleanup journals. It uses both legacy and current ownership attributes.
An additional client has a matching public name but unrelated ownership.

The generated API accepts Gateway deletion through REST and rejects new account
requests. The check waits for partial provider discovery and a saved checkpoint,
then stops and starts the API and provisioner processes. It requires discovery
to finish, every owned provider client to be absent, and all 61 protected journals
to authenticate with closure intent and the same provider identities. It also
requires the account registration scope to be sealed, no new account rows, and
an unchanged unrelated provider client.

Use the `Real provider discovery and cleanup` hosted workflow. Do not run this
application check on the developer workstation. Keycloak and PostgreSQL have
CPU, memory, and process limits. The test has a six-minute deadline; the job has
a twelve-minute deadline. The final cleanup step removes only provider containers
with this test label and this GitHub run ID. PostgreSQL uses the explicit literal
loopback TLS exception. The provider and its generated RPC connection use
verified TLS.

This is an application correctness check, not a production capacity measurement.
It does not restart PostgreSQL or the separate state API fixture, and it does not
claim complete Gateway workload or identity cleanup. Those controllers have
separate complete-workflow gates. The result is pending. A failed or incomplete
run cannot qualify the implementation.


## Initial result

[Run 35211358466](https://github.com/jsell-rh/hypershell-stego/actions/runs/35211358466)
failed on source `11db3e9` with compiler `0057370`. Partial discovery and the
API/provisioner restart completed, but the scope did not seal before the test
deadline. The test took 225.16 seconds. Provider containers were absent after
cleanup, and hosted service cleanup passed. The source archive and compiler pin
match the retained result. See [the failure record](unknown-provider-before-fix-evidence.json).

Source inspection found that common closure selected legacy ownership for an
unknown current client when both ownership formats were configured. A common
STEGO fix is under test. This initial failure alone does not prove that diagnosis
or qualify the fix. The same application workflow must pass after adoption.
