# Main public Gateway result

The [public workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603476)
passed all 11 required tests at source
`881379731d3b78be8344637942b6160e0e138c53`. The complete rendered workflow took
664.98 seconds. Independent checks matched all 1,369 source files and all 415
generated-file hashes across repeated generation and the saved archive.
Compiler and common registry revision remained
`00573709fb15a2a54de4242aa8fdbabee325179a`.

Gateway creation, owner grants, REST and gRPC access, filtered lists, denied
writes, event delivery, process restart, session renewal, key rotation, and
confirmed logout passed. Database and workload recovery preserved SQL identities,
credentials, keys, and provider data. Account creation, token issuance,
revocation, account cleanup, and durable Gateway deletion passed.

The provisioner outage denied two new account requests without creating rows.
Both Gateways recovered after controller restart without account write retries
or changes to SQL and credential identities. The final record matched the
independent capture from the original test Pod at `2026-09-17T09:15:02Z`.

Six browser instances supplied all 48 startup log/span pairs with complete
metrics and no failed pairs. PostgreSQL operations supplied linked logs,
traces, and metrics across restart, cleanup denial, and recovery. The Gateway
console executable matched the checked module. All three saved screenshots
were viewed. They show the active workspace, the invalid JSON error with a
disabled submit button, and selected editor text. No policy was submitted.

Independent cleanup at `2026-09-17T09:18:13Z` found no test runtime, fixtures,
allocations, or API test Jobs. The shared Lease had no holder. The original
Job completed; its UID was `5a06a8c8-e7a3-4323-8540-7acaa14b1cca`.
The evidence archive SHA-256 is
`7beff68ef45c1cfbe99c7a18800b1b0edf5411355929183a73de969a4023559a`.
The startup signal record SHA-256 is
`75d85848658638278582ebe03e5ac25cdab615d3cb663bf183e558286a28bc1e`.

## Main core result

The core job `105137417077` in
[run 35201603844](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603844)
passed at the same exact source. Independent inspection found 307 top-level
passes and 651 passing test events. Four named live tests were excluded from
this suite and have separate test gates. No failed event was found. The
acceptance package took 1,557.094 seconds. The saved core log SHA-256 is
`4e178feb9d37d26b021115e8567f4232e459a56768eb4d211d8a8c6197f916a2`.

The browser, console, and service-image jobs also passed. The first CNPG attempt stopped before resource creation because the saved CI
credential had less than the required 45 minutes left. Independent cleanup at
`2026-09-17T09:20:06Z` found no runtime, fixtures, allocations, volumes, or Lease
holder. Its log SHA-256 is
`49a492a465cf0554ee76ac403a1eb72ab01f9e72db3e3be4d36c313294b889f1`.
The restricted credential was refreshed. Only the failed job was selected for
attempt 2 at the same source. It passed, as recorded below. The Sandbox job is skipped under the user's deferral.
These results do not complete the enterprise goal or establish production
capacity, full application parity, or live Kata isolation.


## Main CNPG result

Attempt 2 of [run 35201603844](https://github.com/jsell-rh/hypershell-stego/actions/runs/35201603844)
passed all 11 required tests at source
`881379731d3b78be8344637942b6160e0e138c53`. The complete Gateway browser workflow
took 733.57 seconds. Independent verification matched all 1,369 source files
and all 415 generated-file hashes before and after the test. The original
application Job UID was `140351cf-52bf-4c69-bf3c-1ea20b820756`.

Primary replacement completed in 67.23 seconds and preserved SQL object IDs,
Gateway keys, credentials, provider data, and installation data. Controllers
recovered a lost workload namespace. Access checks denied unsafe SQL privileges
and kept viewer lists filtered. Both Gateways denied account creation while the
provisioner was unavailable and recovered after controller restart without
account write retries. The live recovery record captured at 09:45:41 UTC
matches the final archive. Browser and REST deletion removed Gateway accounts,
SQL state, namespaces, roles, and keys while the other Gateway and supplied
database remained available as required.

Six browser instances supplied all 48 matching startup log/span pairs, with
metrics and no failed pairs. The console binary matched the qualified module.
All three saved screenshots were viewed: the active workspace, invalid JSON
with disabled submission, and editor selection. No policy was submitted.

The application initially waited for cluster capacity. It then scheduled on a
new node without operator intervention. A prepared secondary replacement action
stopped at its precondition before any DELETE. Original Pod and volume identity
records confirm that no manual rescheduling occurred. This result does not
measure cluster capacity or determine why the new node joined.

Independent cleanup at `2026-09-17T09:49:31Z` found no test runtime, fixtures,
allocations, or API Jobs. Both original database volumes were absent:
`pvc-8c36ad4c-71b9-47d7-93ea-f3e9c91d4bab` and
`pvc-32e5f480-e5b9-465d-b9f7-93398839b2f9`. The shared Lease had no holder.
The evidence archive SHA-256 is
`7449b1c3b5bf07e7a4cd8e601da345e89b902e43a39e4efc0940ec4d3783c380`.
The startup signal record SHA-256 is
`d62f24ef47e98b090b67175ccf081d8192f27b9623e38bf5923f98e7c93d45de`.

This completes the current main common-browser workflow checks. It does not
qualify the later signed compiler transfer change, establish production
capacity, or complete the enterprise goal. Live Kata isolation remains deferred.
