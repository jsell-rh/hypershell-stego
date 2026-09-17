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

The saved first core log confirms `ENOENT` for the old package path. Its
SHA-256 is `41d8e79ea8d51408d4e0194e72f2ca738c79f00bfc00f0927671737d841c0723`.
The [journal recovery check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35194997813)
passed all 28 required checks. Its result log SHA-256 is
`283bf50c094c3f8d6ecd63b6e46a1922ad97a18a71e2a9f038286fde15ce6d43`.

## Complete public Gateway workflow

The corrected [public workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35195368755)
passed all 11 required tests at `af43205`. The complete rendered workflow took
662 seconds. Independent checks matched all 1,363 source hashes and all 415
repeated generated-file hashes. The image executable matched the checked Gateway
module. All three saved screenshots were viewed. The workspace, invalid JSON
message, disabled submit button, and editor selection were visible.

The test covered Gateway creation and grants, REST and gRPC access, event
delivery, restart, confirmed sign-out, SQL isolation, namespace recovery,
account cleanup, and durable Gateway deletion. A provisioner outage denied two
new account requests without creating rows. Recovery preserved SQL and
credential identities. Six browser instances exported all 48 matching startup
log/span pairs with metrics. No failed startup pairs were recorded.

Independent inspection at `2026-09-17T07:57:58Z` found no test runtime,
fixtures, or allocated namespaces and roles. The shared Lease had no holder.
See the [exact source and result record](common-browser-public-evidence.json).

The corrected [full suite](https://github.com/jsell-rh/hypershell-stego/actions/runs/35195371309)
passed its core, browser, console, and service-image jobs. The core log contains
307 top-level passes and 651 passing test events. Four named live tests were
excluded from the core suite. CNPG and Sandbox jobs were not selected. The core
log SHA-256 is `2ef2fd88e328a606221ae4700d495b6bc5e04e186045321b5853cde2caec575d`.

## API restart fixture correction

The separate [API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35197241667)
reported a failure in the cluster deletion restart test. A `rolebinding.created`
event still had a valid lease with 23.525 seconds left. The fixture required an
empty queue within five seconds. The exact claim response was not captured;
this is consistent with a retained claim after cancellation.

The generated runtime already permits recovery after its 30-second lease.
The fixture now uses the existing bounded recovery helper after each restart.
Its total context permits the three recovery windows. Normal delivery checks
retain their five-second bound. No runtime or generated code changed. The API
list now also requires the existing deterministic unfinished-claim test, which
checks that the original event identity survives lease expiry without a manual
reset. All 14 small Job-observation tests passed locally. The corrected API
result and CNPG qualification remain open. The failed run remains part of the
record. These results do not complete the enterprise goal.
