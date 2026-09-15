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
still need CI results.

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
