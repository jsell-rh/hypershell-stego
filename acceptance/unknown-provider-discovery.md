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


## Compiler adoption

The work branch selects compiler and common registry `e206b41`. The immutable
compiler release passed all six source checks and the signed artifact checks.
Its uploaded files were downloaded and compared with the independently verified
package before publication. The common installer then verified the published
release without executing the compiler on the workstation.

[Hosted regeneration 35212905594](https://github.com/jsell-rh/hypershell-stego/actions/runs/35212905594)
passed for all three modules. Independent inspection matched all 414 archived
files after import. Only the generated provider closure, embedded CLI compiler
identity, and three state files changed. The installer result matched the
verified release. See the [generation record](unknown-provider-generation-evidence.json).
The first workflow definition used the runner context before a runner was
available. That definition was corrected before this passing generation run.

The application discovery rerun passed, as recorded below. Complete application
checks remain in progress.
No domain adapter or ownership policy changed for this fix.


## Verified application result

[Run 35213091188](https://github.com/jsell-rh/hypershell-stego/actions/runs/35213091188)
passed at source `762824b` in 119.55 seconds. Independent checks matched the
source archive and compiler pin. The test body, provider fixture, and domain
adapters are unchanged from the failed run.

Twenty durable journals and the discovery checkpoint survived the API and
provisioner restart. Cleanup then removed all 61 owned provider clients. All 61
closure journals authenticated and retained their provider identities. The
account scope sealed, account rows remained absent, and new account requests
were denied before and after restart. The matching-name foreign client remained
unchanged. Provider containers were absent after cleanup, and hosted service
cleanup passed. See the [verified result](unknown-provider-after-fix-evidence.json).

This result proves the discovered failure is fixed through generated common
code for this bounded workflow. It is not a production capacity target or proof
of all enterprise requirements. Full application checks remain in progress.
