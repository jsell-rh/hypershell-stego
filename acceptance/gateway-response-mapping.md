# Gateway response mapping candidate

This candidate uses the signed STEGO compiler `82439300` to generate every
Gateway response field. The compiler pin and reviewed output are included.
Application checks and the complete live workflow remain required.

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
complete live workflow checks remain required. No application acceptance result
is claimed by this candidate.

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
