# Common worker connection settings

The Gateway identity worker, Gateway workload worker, and account provisioner
use the existing STEGO `ControlAPI` configuration group. The workload worker
also uses `ClusterWorker`. These groups already serve the namespace allocator
and Sandbox count worker. This change adds no environment names or generated
configuration types.

The entry points load the selected groups before they construct a connection.
Invalid scalar input returns a private configuration error before certificate
or token file access. Provider constructors still check addresses, trust,
credentials, and access. Their cleanup calls remain in place.

Hypershell retains caller grants, instance identity rules, provider state
journals, optional dashboard dependencies, and OpenShell settings. The separate
optional provisioner connection retains its existing enablement rules.

The new checks supply invalid values for each control connection field in all
three entry points. They require the typed field error and check that it does
not expose supplied values. A separate check requires invalid cluster settings
to fail before the workload worker opens its control connection.

This branch requires hosted application checks and complete workflow evidence
before main promotion. No compiler or application binary was run locally.

The first application check stopped before tests because the recorded worker
input hashes were stale. Hosted [regeneration run 35539887087](https://github.com/jsell-rh/hypershell-stego/actions/runs/35539887087)
refreshed those records. Review checked all 428 output and module files: 427
are unchanged. Only three worker input hashes and their combined digest changed
in the root state file. All 419 output hashes, 41 input hashes and modes, and
three input manifest digests match. The compiler installation record matches
the authenticated release. See the
[generation evidence](control-worker-configuration-generation-evidence.json).
The initial failed checks remain failure records; the refreshed source requires
new application checks.

Corrected source `e0f3d7b` passed
[full run 35540006064](https://github.com/jsell-rh/hypershell-stego/actions/runs/35540006064).
Independent review confirmed 1,176 core cases across 366 top-level tests. All
1,165 prior cases and five recorded exclusions remain. The browser, UI, and
service-image jobs passed; the deferred Sandbox job did not run. The focused
recovery check passed 172 cases across 44 top-level tests and seven packages.
Seven application images passed source, content, registry, and signature review.
See the [hosted evidence](control-worker-configuration-ci-evidence.json).

These results do not include the pending live count and browser checks for this
source. Main promotion remains pending. The earlier service Deployment result
belongs to source `7ed1a7b7` and is recorded separately.


The first three bounded live count attempts did not prove count behavior.
They stopped at generation storage, a missing render endpoint, and a missing
allocator endpoint input, respectively. Independent cleanup passed after each
attempt. The test now uses writable generation storage, verifies the complete
compiler transfer, supplies the generated allocator endpoint settings, and
selects the count binding by role and subject identity.

[The fixture check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35542808955)
passed on test source `d98b673`. It checked repeated rendering of all three
workers and 15 binding-identity cases. Only five test and CI files differ from
application source `e0f3d7b`; production code and generated output are unchanged.
See the [fixture evidence](count-fixture-input-evidence.json). The corrected
live count result and the complete browser workflow remain required before
consumer acceptance.
