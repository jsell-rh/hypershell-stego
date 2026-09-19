# Account cleanup at the target population

Hosted run `35476517343` used source `4ea3e1e` and compiler `ee348b8`.
The fixture had 100 Gateways with 100 accounts each. The selected Gateway's
100 accounts were created through REST. The other 9,900 accounts were seeded.
Keycloak used PostgreSQL and its production `start` mode.

The account cleanup gate passed in 21.2756 seconds after HTTP 202. The scope
sealed at 21.0061 seconds. All 100 account rows and protected journals closed.
All selected provider clients and users were absent. The other 9,900 account
rows and 10,007 provider clients stayed unchanged. Source, compiler, binary,
resource-limit, and test cleanup checks passed. See the
[result record](capacity-tenth-evidence.json).

The fixture files are unchanged from the ninth run, which took 61.5487 seconds.
The application now assigns recovery of deleted Gateway accounts to the retained
Gateway scan. It no longer repeats that work in the deleted-account stream.
Live or absent parents still use account recovery. Complete row and journal
scans, provider inventory, and late-effect checks remain. STEGO still supplies
the common controller and provider runtime.

The owner selection passed 11 cases in journal run `35476194174`. All 41
required tests, 33 RPC status cases, 22 retry cases, and both restart fixtures
passed. See the [journal record](cleanup-owner-journal-evidence.json).

This is one passing account cleanup measurement. It does not prove a latency
distribution, larger or concurrent cleanup, complete workload and database
cleanup, or repeatable compliance with the target. The full application gate
and live 100-account Gateway workflow remain required. The 30-second target
and production quota choices are unchanged.

The completed browser job in full run `35476514171` passed at the same source.
Three runtime instances each supplied all eight startup stages with matching
logs, traces, and metrics. No startup failure pair or out-of-memory event was
observed. Both saved views were reviewed. They show a provisioning Gateway and
an empty account list after deletion. They do not prove live Gateway readiness.
See the [browser record](cleanup-owner-browser-evidence.json).
