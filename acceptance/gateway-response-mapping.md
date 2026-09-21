# Gateway response mapping

This revision accepts runtime source `047f550f`. It uses the signed STEGO compiler
`82439300` to generate every Gateway protobuf response field. The compiler pin
and reviewed output are included.

Hypershell selects `row.CurrentObservations()` before it calls the generated
mapper. This preserves the rules for stale observations and deletion status.
Access checks, domain operations, placement, release selection, and public error
selection remain in the application.

The declaration retains all IDs, metadata, optional field presence, controller
addresses, and the Sandbox count. DNS-name conversion has the existing domain
limits of 128 entries and 253 decoded bytes per entry. Its 256 KiB encoded limit
also permits the largest escaped input allowed by those limits. STEGO does not
select or validate DNS policy.

The added checks cover every response field, protobuf JSON and wire data, stale
and deleting views, malformed stored lists, exact list limits, and private error
responses. The existing REST, gRPC, access, event, restart, regeneration, and
complete live workflow checks remain required for future runtime changes.

Hosted generation checked all 431 generated and module files, 422 output hashes,
and 41 input hashes. Six files changed: the Gateway mapper, its common JSON
helper, the CLI compiler identity, and three generation records. The catalog
mapping functions are unchanged. The JSON helper matches the released template.
See the [generation evidence](gateway-mapping-generation-evidence.json).

The generation-input branch retained the previous output. Three checks failed
at the generated-source comparison before runtime acceptance; the journal job
also had no test results to upload. Those failures remain recorded. An initial
local reader ran before collection was complete and found no result file. The
same reader passed after the original collector saved the result. No CI job was
repeated, and no compiler or application binary ran on the workstation.

The candidate passed 1,335 core cases across 380 test roots, including all
1,314 prior cases. All 141 focused cases across 12 roots, five restart cases,
and 11 provider roots passed. Regeneration matched 431 generated and module
files, 422 output hashes, and 41 input hashes. The patch was empty. All seven
application images passed independent content and signature checks. See the
[application evidence](gateway-mapping-application-evidence.json).

The complete live Gateway workflow passed all 11 required tests. The browser
test took 562.45 seconds. Independent review checked 1,713 source files, 432
generation hashes, exact compiler bytes, REST and gRPC access, events, restart,
correlated logs, metrics, traces, and four browser images. All seven evidence
readers completed without error. Cleanup removed both test fixtures, released
the Lease, and preserved all 32 standing resources. See the
[browser evidence](gateway-mapping-browser-evidence.json).

Normal cleanup with 100 live accounts took an observed 33.04 seconds. The
30-second target remains unmet. These results do not prove production capacity,
live Kata isolation, native Sandbox packet isolation, or OpenShell Sandbox
execution. They do not explain the historical event timeout. REST conversion,
grant mapping, worker configuration, and the remaining enterprise requirements
remain open. Exact main checks are required after promotion.

The preceding catalog main `ccf350b1` also passed independent review of its full
suite, live API, focused cases, regeneration, seven images, and cleanup. Its
[main evidence](catalog-mapping-main-evidence.json) has a separate source and
compiler identity. It does not stand in for this candidate's checks.
