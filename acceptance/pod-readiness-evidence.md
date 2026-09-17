# Current Pod readiness evidence

The main browser run 35248320996 failed at source
`110d9c4eeb5d830ec8b8d1d03a780a5e5fbfbc47`. The console Pod was not ready
when the test checked the baseline before removal of public network access.
The API and Deployment readiness observations came from earlier reads.
The saved result is a failure. Independent cleanup passed.

The test now repeats valid pending Pod observations within its existing
180-second readiness wait. It saves account evidence only after both current
workload Pods are ready. Account and token errors, incomplete inventories,
multiple current Pods, and invalid readiness data still cause failure.
Terminating Pods must also use the required account and token setting.

[Focused run 35251253553](https://github.com/jsell-rh/hypershell-stego/actions/runs/35251253553)
passed at `46e9273`. Independent checks confirmed all 54 Pod cases, 12 account
dependency cases, both race-test packages, and the source inventory. The rendered
installation and all six policy manifests are unchanged. See the
[focused evidence](pod-readiness-focused-evidence.json).

The focused result covers the observer. The complete main results below cover
the application workflow.

The correction is on main at `c253c04`. The hosted browser check passed in
91.19 seconds. All three browser instances supplied eight startup stages with
logs, traces, and metrics. Both screenshots were reviewed. Seven generated image
checks and 231 UI tests also passed. See the
[main evidence](pod-readiness-main-evidence.json). The core suite passed 738 cases, including 313 top-level tests. All 52 API
checks and all 11 live browser checks passed. The main browser workflow took
660.38 seconds. Independent checks matched 1,447 source files, 416 generated
hashes, the published compiler, and the console image. Three live dashboard
images were reviewed.

The live record contains 24 ready-Pod observations across two Gateways and three
namespace instances. It confirms separate account names, new account and Pod
UIDs after namespace replacement, explicit token settings, and denied worker
account writes. Both cluster tests completed. Independent cleanup found no
remaining test resources and a free shared lock.

The old failed result is retained. This pass does not establish why its console
Pod lost readiness. Sandbox isolation and the separate control-account reuse
audit remain open.
