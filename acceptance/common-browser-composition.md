# Common browser composition

Source `17f6295` removes the local Gateway browser archetype. Both consoles use
STEGO's common `browser-service` archetype and its pinned Git registry. Only
the API application archetype remains local. The management UI now uses the
console module's generated telemetry package. The API no longer generates that
package. Gateway routes, upstream assets, and access policy remain application
inputs.

All modules select compiler and registry revision
`00573709fb15a2a54de4242aa8fdbabee325179a`. The compiler's
[main signature check](https://github.com/jsell-rh/stego/actions/runs/35194639679)
passed. Independent verification accepted both signatures and the exact source
and signer. The compiler SHA-256 is
`e5894237467e81c6a3e7a7c8abd436192a74174726384c2f30716e63db3101bb`.
Generation used that verified compiler and the checked official SDK.

## Generation and asset checks

All three modules passed apply, dependency resolution, repeated apply, and drift
checks. Repeated output and state hashes matched. The moved client files are
byte-for-byte identical. Of 217 generated Go files, only the compiler identity
record and startup component indices changed. The
[generation record](common-browser-generation.json) retains the checks and limits.

The [management asset build](https://github.com/jsell-rh/hypershell-stego/actions/runs/35194997810)
passed at `17f6295`. Independent checks verified the exact source archive,
compiler pin, ZIP entries, and checksum. The 54-entry, 852,967-byte archive
matches the committed archive. Its SHA-256 is
`677404ba290aa54e40fe78e656a6cbd31df273820036e7453b2cbd24eaaac6ec`.
No replacement of the UI asset archive was required.

The [Gateway module check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35194997844)
also passed. All 129 selected source files matched its archive. Repeated
generation, dependency scans, both entry-point builds, private deployment
checks, and image verification passed. The executable matches the previously
qualified executable, with SHA-256
`07a677758dcc555968926360cdd2fb4c1227b4a5835d8fc6c117774211babc06`.

## Acceptance fixture correction

The first full suite at `17f6295` failed its core and browser setup because the
acceptance fixture still selected `out/browsertelemetry`. Source `af43205`
changes the fixture's Go path, npm declaration, and lockfile to
`console/out/browsertelemetry`. These are its only source changes. The asset
and Gateway module results above retain their scope.

The first public run was cancelled because it had the same invalid input.
Independent inspection at `2026-09-17T07:36:04Z` found no test Jobs, Pods,
Deployments, StatefulSets, Services, or Routes in either test namespace. The
live-test Lease had no holder. The cancelled run is not a pass.

The corrected [full suite](https://github.com/jsell-rh/hypershell-stego/actions/runs/35195371309)
and [public workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35195368755)
are separate required results. Complete application, API, CNPG, restart, and
cleanup qualification remains open. These generation and module checks do not
establish full application behavior or complete the enterprise goal.
