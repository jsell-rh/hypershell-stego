# Public Gateway workflow

The user selected TLS passthrough and an operator-selected certificate issuer
on 2026-09-15. TLS ends at the Gateway. Clients must verify its hostname and
certificate chain. Keep certificate verification enabled for public and internal
connections. The selected issuer must support the names that it signs.

The user also made `route_address` controller-owned. Owner create and patch
requests must reject this field, including an empty value. Responses retain it.
Only the assigned controller can publish or clear the observed address. It must
verify route ownership, workload state, and the current resource revision.

The API now excludes `route_address` from owner REST and CLI inputs. A gRPC
controller write requires an exact `observe.endpoint` grant for the stored
cluster and the current revision. STEGO's generated observation group hides an
address from an older desired generation. Focused policy and request-decoder
checks pass. The complete acceptance package compiles; live transport checks
still need CI results. The restricted API gate now requires the controller
write-grant test, including address ownership and stale observations.

The complete browser workflow passed at `59a6d32` with compiler `a355306`.
Source, generation, artifacts, rendered pages, and independent cleanup reads
were checked. See [the evidence](controller-endpoint-browser-evidence.json).
The run uses an external PostgreSQL container and an internal Gateway endpoint.
It does not verify the later public TLS changes. The new 32-test API gate has
not passed: the available CI login had too little time left to start the run.

The console archive gate at `25927c2` found source drift. Its frontend inputs
match `59a6d32`. Thus the browser pass establishes behavior of the committed
archive, but not a fresh build from that source. The corrected archive requires
a new console and complete browser gate.

Commit `01fa021` replaces the archive with the checked CI candidate and
regenerates the console. The application bundle differs only by removal of
`route_address` from the owner patch schema. The other changes are asset names
and manifest references. See [the archive evidence](console-endpoint-assets-evidence.json).
Full CI `34974186524`, API `34974186030`, and browser `34974186012` remain
required for this source. The cluster runs need a renewed CI login.

The full public workflow remains incomplete. The browser checks use an internal
Service endpoint. The connection panel still shows loading placeholders. Route
verification, certificate selection, and public connection evidence remain open.

The complete public workflow must create a Gateway through the console, publish
a verified address, show a usable command, and execute an authenticated RPC
through the router. Denied users and invalid tokens must remain denied. Route
failure must remove readiness; recovery and restart must preserve SQL data and
access rules. Gateway deletion must remove its route and preserve other Gateways.
Generation and cleanup checks remain required under the shared live-test Lease.

STEGO supplies common resource clients, ownership and readiness checks, bounded
TLS transport, telemetry, and controller behavior. Hypershell supplies Gateway
assignment, hostname policy, and the API observation mapping. See the
[shared acceptance contract](https://github.com/jsell-rh/stego/blob/main/specs/hypershell-external-connection.md).
