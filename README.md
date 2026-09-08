This repository is the test bed for a STEGO-based Hypershell variant.

STEGO must provide common service infrastructure and generated contracts.
Hypershell must supply its unique business rules and application workflows
through explicit extension points. No STEGO component may depend on a Hypershell
entity name or application rule.

The variant now has a Gateway domain service over STEGO-generated storage and
event delivery. Its generated process now serves Gateway creation, retrieval,
and filtered lists over REST and gRPC. It delivers committed events through mutual TLS.
PostgreSQL tests check atomic owner grants, verified identities, denied reads,
rollback, and restart. Tests read the same resources across both transports.
Full REST query support and the other application workflows remain open. See
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
exercise the Gateway domain service and generated event process. CI requires
PostgreSQL, the race detector, and a regeneration check.

The generated entry point is `go run ./out`. It requires the database schema,
the `DATABASE_URL` setting, a verified JWT issuer configuration, and a reachable
Kafka broker with TLS. gRPC also requires `STEGO_GRPC_TLS_CERT` and
`STEGO_GRPC_TLS_KEY`. It uses TLS 1.3 and reads `STEGO_GRPC_ADDR`, which defaults
to `127.0.0.1:9090`. HTTP reads `PORT`, which defaults to 8080. Application
startup does not apply migrations. The current REST list supports `page` and
`size`, including a zero-size count request, with a maximum page size of 100.
Search, custom ordering, sparse fields, updates, and deletion remain open work.

The gRPC list defaults to page 1 and size 20. Sizes from 1 to 500 are valid;
other sizes select the default. Its metadata size is the requested page size.
REST metadata size is the returned item count. Gateway update, delete, watch,
and count adjustment methods currently return `Unimplemented`. Long-lived
streams need a separate lifetime policy before watch support can be added.
