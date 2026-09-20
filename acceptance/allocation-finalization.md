# Gateway allocation cleanup

Gateway deletion must wait for the namespace allocator. Workload and SQL
cleanup finish before the allocator removes retained state namespaces. Their
completion records do not prove that those namespaces are absent.

The application declares the targeted `allocation` cleanup owner. The allocator
uses the existing STEGO observation runtime and generated cleanup storage. It
checks the Gateway, Sandbox, console state, and Gateway state allocations before
it records completion. The commit uses the resource version read before removal.
A pending result or a failed commit does not indicate completion.

The allocator requires an exact `Gateway` / `cleanup.allocation` grant for its
ManagedCluster. Workload and identity grants do not authorize this observation.
The generated cleanup summary also supplies the allocator's cleanup metrics.

The complete browser workflow and ten hosted checks passed at source
`6061469`. The separate API gate remains required before promotion.
The new declaration changes the schema generation. Existing installations must
remain rejected until an explicit schema transition is registered and tested.
Do not change generation records by hand to bypass that check.

The live regression uses the existing SQL permission fault. It stops every
allocator Pod before it restores SQL access. Other cleanup owners can then
finish while the state namespaces remain. REST and gRPC must both return the
Gateway in the deleting state. A new allocator process must finish removal and
finalization. The collector requires `allocation-finalization.json`. The live check passed
in run `35485013231`, including the stopped allocator and its replacement.

Full run `35482437428` at `ecb3160` failed three catalog and CLI tests. Their
controlled provider fixtures recorded SQL and workload cleanup but omitted the
new allocation owner. The API correctly refused to delete the managed cluster
with HTTP 409. The corrected fixtures supply an exact allocation grant and
record completion through the versioned cleanup RPC. The placement test also
requires HTTP 409 after SQL and workload cleanup, before allocation completion.
The focused CI check includes all three failed tests. The corrected full suite passed 963 cases across 337 top-level tests. The
focused check passed all three previously failed tests. The earlier failure
remains part of the result record.

## Live result

Run [35485013231](https://github.com/jsell-rh/hypershell-stego/actions/runs/35485013231)
passed all 11 required tests. Independent checks matched 1,568 source files,
421 generation hashes, the signed compiler and its bytes in the test Pod,
24 ready-Pod account observations, and four inspected UI images. The workflow
covered login, Gateway creation, grants, filtered and denied access, REST and
gRPC, events, process and namespace replacement, database restart, telemetry,
and deletion. See the [source-specific record](allocation-finalization-evidence.json).

With the allocator stopped, the other cleanup owners completed while the two
state namespaces remained. REST and gRPC both returned the deleting Gateway.
After the allocator restarted, removal completed before finalization. The
normal deletion record also contains all 15 required phase observations. No
namespace was observed present after finalization; finalization did not return
to pending.

Independent reads confirmed that both test namespaces had no test workloads,
all browser fixtures and allocated resources were absent, and the shared lease
was free. All 32 standing installation objects matched their recorded values.
The read-only checks did not change the installation.

## Timing and open work

The normal deletion used 100 accounts created through REST, each with verified
token issuance. Complete cleanup proof took 58.1439 seconds. Gateway state was
first observed absent at 53.9290 seconds; allocation cleanup was recorded at
56.0171 seconds; finalization was observed at 57.0312 seconds. These are
sequential read-return observations, not exact provider transition times.
The 30-second whole-Gateway target remains unproved.

The account cleanup flag returned to pending 14 times. The combined cleanup
predicate and finalization had no observed regressions. A separate review must
distinguish an incomplete repeated scan from evidence of a new provider effect.
This result does not establish the cause of namespace removal latency.

The bounded hosted capacity check removed 100 accounts with 10,000 background
clients in 21.9520 seconds. It verified process limits, source and binary
identity, provider records, and cleanup. This account-only result does not
include Gateway workloads and does not prove production capacity.

The provider restart verifier initially required exactly 21 journals when the
test stopped. This run had 22. The test requires at least 20 and fewer than 61,
then requires all 61 protected identities after restart. The corrected verifier
uses those existing bounds and retains every other check. No application test
was changed to accept this result.

Existing schema upgrades, distributed fencing, backup and restore, production
capacity, full application parity, and the remaining enterprise requirements
remain open. Live Kata and native Sandbox traffic remain outside this result.
The upstream Agent Sandbox controller remains trusted cluster infrastructure.
