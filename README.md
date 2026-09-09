This repository is the test bed for a STEGO-based Hypershell variant.

STEGO must provide common service infrastructure and generated contracts.
Hypershell must supply its unique business rules and application workflows
through explicit extension points. No STEGO component may depend on a Hypershell
entity name or application rule.

The variant now has a Gateway domain service over STEGO-generated storage and
event delivery. Its generated process now serves Gateway creation, retrieval,
patches, deletion, and filtered lists over REST and gRPC. Gateway watch streams
return current authorized data. The runtime also delivers durable events through
mutual TLS.
PostgreSQL tests check atomic owner grants, verified identities, denied reads,
rollback, and restart. Tests read the same resources across both transports.
REST search and ordering are implemented. Field selection, related-resource
search, and the other application workflows remain open. See
[the checks](acceptance/README.md).

The reference REST and gRPC contracts are under `contracts/reference/`. `contracts/upstream.json`
records their source revision and SHA-256 hashes. These files are acceptance
inputs. STEGO also uses the Gateway and common protobuf files as generation inputs. The source is Apache-2.0 licensed; see
`LICENSE`.

The compatibility target includes:

| Area | Required behavior |
| --- | --- |
| REST | Resource fields, paths, methods, pagination, search, errors, and field visibility |
| gRPC | Messages, service methods, watch streams, and authorization parity |
| Security | OIDC verification, platform roles, resource ownership, filtered lists, and opaque denied reads |
| Storage | PostgreSQL constraints, atomic owner grants, concurrency, and explicit migrations |
| Events | Committed resource events, delete events, reconnect, and bounded delivery |
| Service accounts | Creator ownership, role limits, lifecycle, secret handling, and provisioning |
| Control plane | Gateway, cluster, database, network, and release reconciliation |
| Clients | SDKs, CLI commands, web workflows, and authentication |
| Operations | Health, readiness, metrics, tracing, limits, shutdown, and deployment checks |

Compiler correctness and reusable infrastructure changes belong in STEGO.
Behavioral acceptance tests and Hypershell domain code belong here. Passing
compilation alone does not establish compatibility or production readiness.

Run `scripts/generate.sh` to regenerate with the pinned STEGO compiler. Run
`scripts/generate.sh --check` from a clean checkout to check committed output.

Run `go test ./...` to verify the source hashes, validate OpenAPI references, and
compile the protobuf contracts. The checks cover 37 REST operations, 41 gRPC
methods, and six server watch streams. They also check gateway field ownership,
reserved wire numbers, and the restriction on returning service-account secrets.
Run `go run ./cmd/contracts` to print the operation inventory as JSON. Contract
resolution uses embedded files and cannot fetch remote schemas.

The contract tests validate the reference inputs. The separate acceptance tests
exercise the Gateway domain service and generated event process. Run
`scripts/check-gateway.sh` with `STEGO_TEST_POSTGRES_DSN` set for the complete
[Gateway gate](acceptance/gateway-workflow.md). CI uses the same command. It
requires PostgreSQL, the race detector, and a regeneration check.

The generated entry point is `go run ./out`. It requires the database schema,
the `DATABASE_URL` setting, a verified JWT issuer configuration, and a reachable
Kafka broker with TLS. gRPC also requires `STEGO_GRPC_TLS_CERT` and
`STEGO_GRPC_TLS_KEY`. It uses TLS 1.3 and reads `STEGO_GRPC_ADDR`, which defaults
to `127.0.0.1:9090`. HTTP reads `PORT`, which defaults to 8080. Application
startup does not apply migrations. The REST list supports `page`, `size`, `search`, and `orderBy`, including a
zero-size count request, with a maximum page size of 100. Search and ordering use
declared fields. Sparse fields and related-resource search remain open work.

The gRPC list defaults to page 1 and size 20. Sizes from 1 to 500 are valid;
other sizes select the default. Its metadata size is the requested page size.
The domain service limits page numbers to 1,000,000 for both transports.
REST metadata size is the returned item count.

`WatchGateways` subscribes before it sends response headers. The client must
wait for those headers, list current Gateways, then apply watch events. Repeat
this sequence after any stream failure. The reference protocol has no cursor
or history. An event contains the current authorized resource state, not a
historical snapshot. A delete event uses the stored tombstone and live grants.
Revoked grants stop further data delivery; clients must also clear stale local
state when access changes or they repeat the initial list.

STEGO supplies the event source and transport limits. Hypershell supplies event
selection, Gateway field mapping, and access checks. The source uses one dedicated
PostgreSQL LISTEN session per process. Use a direct database connection or session
pooling. Transaction pooling is not supported. An event-source failure stops the
process, so a broken stream cannot appear healthy. Kafka retains durable events
across process downtime; watch clients recover through a new list.

Streams stop at token expiry or after five minutes by default.
`STEGO_GRPC_STREAM_TIMEOUT` accepts one second through 30 minutes. The separate
stream limit is 32 per process and four per verified subject. A slow client that
blocks I/O for ten seconds loses its TCP connection; other calls on that
connection must reconnect. `STEGO_GRPC_STREAM_IO_TIMEOUT` accepts one through
ten seconds. The runtime accepts at most 128 connections. These bounds do not
establish production capacity.

Gateway patches require an owner grant on that Gateway. Admin status alone does
not allow a patch. Owners and admins can delete a Gateway. Denied mutations
return the same not-found result as a missing Gateway. Each mutation checks
access and commits the resource and its event together. Patches use a serializable
transaction. Deletion locks the Gateway row against account reservations.
A concurrent write can produce HTTP 409 or gRPC `Aborted`; callers must retry
from the start. STEGO does not replay the transaction callback.

A patch preserves omitted fields. Null fields and an empty `server_dns_names`
list also preserve the stored values, as in the reference. Placement owns
`database_id` and `namespace`. Patches cannot set `active_sandbox_count`.
The credential driver cannot change after a nonempty value has been stored.
Both transports apply `supervisor_image` and `credential_driver` from their
declared request contracts. The reference gRPC handler omits these assignments.

`HYPERSHELL_CONTROL_PLANE_SUBJECTS` is an optional JSON array of up to 32 token
subjects. The token verifier must first verify the configured issuer, audience,
key, and expiry. Only subjects in this list can set `console_address` through
gRPC. They can also perform Gateway operations without user grants. The list is
empty by default. Usernames and role names do not grant this access. REST has no
console-address patch field. This subject allowlist is the current design
assumption; the requested identity-policy decision remains open.

Gateway deletion refuses a Gateway that still has live service-account metadata.
Delete those accounts first. The shared Gateway row lock prevents a concurrent
account reservation from bypassing this guard. Automatic provider cleanup within
Gateway deletion remains required for full reference compatibility.

The gRPC `AdjustActiveSandboxCount` and `SetActiveSandboxCount` methods now use
STEGO's resource-locking transaction. Only configured control-plane subjects
can call them. Owners, viewers, creators, and admins have no implicit access.
The count is floored at zero. An unset count becomes zero on the first operation.
An unchanged stored value emits no event. A missing or deleted namespace returns
zero and emits no event. A result above the signed 32-bit limit returns gRPC
`OutOfRange` without a state change.

Each changed count and its update event commit together. Concurrent increments
wait for the resource lock and read the current value. Count changes update the
resource timestamp through generated storage; unchanged values leave it intact.
REST and gRPC Gateway reads expose the same count. Relative adjustments do not
have request deduplication: after a connection failure with an uncertain result,
a caller must reconcile the observed count instead of assuming a retry is safe.
The control-plane reconciliation workflow still needs to be ported.

Service-account create, list, get, revoke, and delete now run through the generated
HTTP process and a TLS provisioner client. Only creation returns a client secret.
Pending operations recover after restart. Recovery also enforces expiration and
the creator's current Gateway grant. See [service-account evidence and limits](acceptance/service-accounts.md).
The [Keycloak provider](acceptance/keycloak.md) now runs through the generated
TLS transport. CI tests actual token issuance and revocation with a pinned
Keycloak container. Complete list filters, automatic Gateway cleanup, provider
failure ordering, production capacity, and client ports remain open.
