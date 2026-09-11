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
REST search, ordering, and [list field selection](acceptance/field-selection.md)
are implemented. Related-resource search and other application workflows remain
open. See [the checks](acceptance/README.md).
Shared [gRPC tracing](acceptance/grpc-tracing.md) covers Gateway calls and watch
lifetime. The [request observability gate](acceptance/request-observability.md)
now verifies correlated logs, metrics, and traces, including denied reads, active
streams, restart, and collector loss. Shared [service logging](acceptance/service-logging.md)
adds local JSON and OTLP runtime lifecycle events, including operation without
a collector. [Controller telemetry](acceptance/controller-observability.md) now
covers shared keyed actions, scans, watch sessions, retries, and queue state.
[Instance identity](acceptance/telemetry-instance-identity.md) keeps request
counts separate across API replicas and runtime replacements. Complete process
logging remains open.
Shared [HTTP client telemetry](acceptance/http-client-observability.md) now
connects API requests, provider RPC calls, and real Keycloak HTTP calls across
restart. Logs, metrics, spans, and propagation come from STEGO.
Shared [database telemetry](acceptance/database-observability.md) continues API
traces into generated PostgreSQL storage. Its Gateway test checks atomic writes,
rollback, filtered access, event delivery, and REST/gRPC reads after restart.
The [pool-pressure gate](acceptance/database-pool-bounds.md) checks bounded
connections, request cancellation while waiting, and recovery after restart.

The first [generated CLI workflow](acceptance/generated-cli.md) now builds from
`out/cli/cmd`. It loads a private token file and creates, reads, lists, and deletes
Gateways over verified HTTPS. It shares STEGO's HTTP client with the service
components. [OIDC login](acceptance/oidc-cli.md) also uses the generated runtime.
Shared [CLI telemetry](acceptance/cli-observability.md) supplies command logs,
metrics, and traces. Generated HTTP calls continue the command trace into the API.
The complete client port remains open.

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
The [compiler preflight checks](acceptance/compiler-preflight.md) reject invalid
component inputs before rendering and retain the checked source snapshots.

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
the `DATABASE_URL` or `DATABASE_URL_FILE` setting, a verified JWT issuer configuration, and a reachable
Kafka broker with TLS. The service database requires `sslmode=verify-full` and
a trusted server certificate. Set `sslrootcert` for a private certificate
authority. See [database TLS](acceptance/database-tls.md) for the test exception
and the pool and event-listener checks. gRPC also requires `STEGO_GRPC_TLS_CERT` and
`STEGO_GRPC_TLS_KEY`. It uses TLS 1.3 and reads `STEGO_GRPC_ADDR`, which defaults
to `127.0.0.1:9090`. HTTP reads `PORT`, which defaults to 8080. Application
startup does not apply migrations. The REST list supports `page`, `size`, `search`, and `orderBy`, including a
zero-size count request, with a maximum page size of 100. Search and ordering use
declared fields. The `fields` parameter selects public fields in list items.
Related-resource search remains open work.

The [database secret source gate](acceptance/database-secret-source.md) checks
mounted credentials, safe source failures, and password changes across restart.
The file source uses the same generated TLS pool and event listener.

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
assumption; the requested identity-policy decision remains open. Cleanup writes
also require [explicit owner and target grants](acceptance/cleanup-permissions.md)
in `HYPERSHELL_CLEANUP_GRANTS`. Missing grants deny these writes.
Conditional Gateway patches also require [field-group and target grants](acceptance/controller-write-permissions.md)
in `HYPERSHELL_CONTROLLER_WRITE_GRANTS`. A configured subject alone cannot patch
Gateway fields. Workload status, OIDC settings, and console address each require
a separate operation grant. Other controller patch fields are denied.
Conditional database patches require a
[provider observation grant](acceptance/database-write-permissions.md) in the
same setting. Only `status` and `connection_secret` are permitted, with the
stored provider name as the grant target. A controller cannot change desired
database settings through this path.

Gateway deletion removes related provider clients before it commits the Gateway,
account metadata, cleanup audits, and deletion event. The Gateway row lock
prevents concurrent account creation from escaping cleanup. Provider failure
returns HTTP 503 or gRPC `Unavailable` and keeps the Gateway. See the
[Gateway account cleanup workflow](acceptance/gateway-account-cleanup.md).

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
The sandbox-count controller reads a Pod watch and writes absolute counts.
It restores counts after restart and repairs drift from its local cache. The
count is advisory and does not prevent Gateway deletion. See the
[count workflow and limits](acceptance/sandbox-counts.md).

Service-account create, list, get, revoke, and delete now run through the generated
HTTP process and a TLS provisioner client. Only creation returns a client secret.
Pending operations recover after restart. Recovery also enforces expiration and
the creator's current Gateway grant. See [service-account evidence and limits](acceptance/service-accounts.md).
The [Keycloak provider](acceptance/keycloak.md) now runs through the generated
TLS transport. CI tests actual token issuance and revocation with a pinned
Keycloak container. Revocation removes the provider identity and retains its
account record and audit history. A failure test proves that a delayed enable
cannot undo revocation after database connection loss. Account discovery now
supports status, literal search, and ordering through
the [discovery workflow](acceptance/service-account-discovery.md). Other provider
failure cases, production capacity, and client ports remain open.

Recovery also removes clients created after their initial cleanup. It retains
deleted account records for repeated cleanup by stable IDs, including after
Gateway deletion and process restart. Normal API queries exclude these records.
Large-history cleanup capacity and bounded retention remain open.

The provider now requires a trusted Gateway ID binding on each Keycloak Gateway
client. A real API test exposed, then verified the fix for, a foreign-audience
access defect. Failed role reduction now queues terminal revocation, including
when OIDC settings are invalid or the provider binding is lost. The tests check
restart and restored owner access. The identity controller now creates these
bindings. Existing-client migration and production recovery latency remain open.

The workflow also exposed a data race during GORM model initialization. The
compiler now prepares model metadata before it starts concurrent application
work. This runs with external migrations and changes no database tables.
The generated store constructor returns an error, which startup and the test
fixtures now handle.

The [Gateway identity workflow](acceptance/gateway-identity.md) adds a separate
controller over generated gRPC and HTTPS clients. It creates trusted Keycloak
bindings from Gateway state and recovers after API or controller restart.
The identity controller does not deploy workloads or set Gateway health.

Gateway identity and workload recovery use STEGO's generated cursor scanner.
It checks each complete page before dispatch, limits page requests, and applies
request deadlines. Both controllers scan live and retained deleted Gateway IDs
through one private API mapping. That mapping checks canonical, nonzero KSUIDs;
the API requires a configured control-plane subject. Actions still read current
state and enforce the domain rules. Invalid scan contracts stop the controllers.
Storage and replay adapters still need generation in STEGO.

The workload controller now uses [generated keyed scheduling](acceptance/gateway-scheduling.md)
with four workers. A blocked Gateway action does not stop another admitted
Gateway from completing cleanup. Actions for the same Gateway remain serial
within that controller process.

The [reconciliation contract review](https://github.com/jsell-rh/stego/blob/main/specs/reconciliation-contract-review.md)
records remaining gaps against Hypershell PR 200. Gateway workload and identity
controllers now use generated resource revisions for conditional status writes.
The [revision acceptance test](acceptance/gateway-revisions.md) rejects an older
observation after a REST desired-state change and checks event rollback and
restart. Gateway now uses generated desired generations and a workload
observation group. Phase and status are controller-owned. Reads, status search,
and credential readiness checks reject stale observations. The database
controller now also requires [revision preconditions](acceptance/database-observations.md)
for its writes. Database generations, field ownership, per-subject group
authority, cleanup for other resources, and controller metrics remain open.
Database provider actions now read current retained state. Failed reads and
missing deletion evidence stop cleanup. Event data alone cannot permit deletion.
The provider records [durable cleanup observations](acceptance/database-cleanup.md)
and continues periodic checks after success. Late effects reopen pending cleanup.
Gateway login identity now uses [the same cleanup contract](acceptance/gateway-identity-cleanup.md).
The controller confirms provider absence and checks again after completion.
Workload cleanup now [retains each cluster target](acceptance/gateway-target-cleanup.md).
A former cluster can complete its own cleanup without completing another cluster.
Database placement history and parent finalization remain open.
These correctness requirements take priority over recovery-query optimization.

[Persistent user identity](acceptance/user-identity.md) now uses the verified
issuer and subject. Username changes preserve grants; username reuse cannot
transfer them. Existing databases need the explicit identity migration and a
trusted mapping for legacy users before access can be preserved.

[Gateway user login](acceptance/gateway-user-login.md) now completes a real
browser login and PKCE exchange. The controller maps current grants by verified
issuer and subject. Tests cover role union, profile reuse, removal, and restart.
Already issued tokens retain their claims; online revocation remains open.

Grant discovery now uses REST lists and the reference gRPC list and watch service.
The generated runtime sends initial owner-grant events and replays active grants
after restart. See [the evidence and limits](acceptance/grant-discovery.md).

The [role catalog](acceptance/role-catalog.md) now supplies role IDs through REST.
The browser grant workflow uses this API. Stable IDs, migration recovery,
authentication, access limits, and restart have acceptance checks.

The [self-identity route](acceptance/current-user.md) lets a signed-in recipient
obtain their stored user ID. The real browser sharing test now obtains both
recipient and role IDs through REST. A user directory remains a separate policy
decision.

[Global role synchronization](acceptance/global-roles.md) now follows verified
claims through REST and gRPC. Removal preserves Gateway ownership. The workflow
covers event delivery, old streams, failures, real provider changes, and restart.

The [placement catalog workflow](acceptance/placement-catalog.md) now creates cluster, release,
and database records through the generated API before Gateway creation. REST,
gRPC, access checks, atomic events, watches, restart, and migration checks cover
this path. Catalog writes require a platform admin or configured controller.
The Gateway workload gate below verifies deployment.

The [deployment placement workflow](acceptance/deployment-placement.md) now makes a separate
database record for each Gateway by default. The database, Gateway, owner grant,
and three events commit together. Set `DATABASE_PROVIDER=cnpg` explicitly to use
the shared CNPG path. REST requires a `database_id` property but accepts an empty
string. Apply migration 000007 before the new API starts. The database workload and cleanup now have a separate acceptance gate.

The [database workload workflow](acceptance/database-workflow.md) now provisions PostgreSQL on Kubernetes
from a Gateway creation event. It checks verified TLS, limited database roles,
persistent data, stable credentials, foreign namespace denial, and cleanup after
offline deletion. REST, generated gRPC, generated HTTPS, restart, and regeneration
are part of this path. The cluster test has its own required CI job. Production
database operations remain open.


The [Gateway workload gate](acceptance/gateway-workload.md) runs the actual
OpenShell Gateway with PostgreSQL and Keycloak. It checks owner and viewer
access, provider data, database and Gateway restart, namespace replacement,
stable keys, and recovery after offline deletion. Run
`scripts/check-gateway-workload.sh` with the test PostgreSQL connection set.

The experimental [sandbox gate](acceptance/sandbox-workflow.md) adds sandbox
creation and command execution under Kata. It checks admission denials, client
key protection, a hard process limit, stored data after Gateway restart, and
cleanup. Run `scripts/check-sandbox-workload.sh` on Linux amd64 with usable KVM.
The runtime choice and production isolation requirements remain open.

The generated CLI also supports [service accounts](acceptance/service-account-cli.md).
It creates a private credential file, retrieves and lists accounts, and revokes
or deletes them through the generated runtime.

The [grant CLI workflow](acceptance/grant-cli.md) now changes Gateway access
through generated commands. See the [CLI port status](acceptance/cli-port.md)
for the remaining reference behavior.

The [catalog CLI workflow](acceptance/catalog-cli.md) creates placement records
and Gateways under CNPG and default deployment modes. It also tests protected
deletion and event rollback.

The [Gateway-network workflow](acceptance/gateway-networks.md) now checks network
record CRUD, access, CLI commands, watch events, rollback, restart, and database
upgrade. It preserves the reference metadata behavior. Neither this port nor
the reference network controller sets up tunnels. The complete Hypershell port
and production acceptance remain open.

The [generated apply workflow](acceptance/cli-apply.md) now creates and patches
catalogs and Gateways from resource documents. It checks dry runs without API
contact, complete preflight, partial failures, access, events, restart, and both
database modes. [RoleBinding apply](acceptance/cli-immutable-apply.md) also creates
grants and recognizes an exact existing match without a write. The common
runtime is supplied by STEGO. Kustomize rendering and the complete CLI port
remain open.
Recovery now uses [generated storage cursors](acceptance/storage-cursors.md) for
Gateway IDs, deleted databases, and service accounts. These queries preserve
access and state filters without unused counts or application-built ID searches.

Controllers use [generated observation budgets](acceptance/observation-deadlines.md)
to leave time for a conditional status or cleanup write after a provider timeout.
Five workflows check failure, event delivery, API restart, and recovery.

[Database recovery](acceptance/database-recovery.md) now uses one generated
cursor stream for live and retained deleted IDs. Public list shapes remain
unchanged. The controller checks the replay scope before it accepts any IDs.

The compiler now protects [generated Go symbol bindings](https://github.com/jsell-rh/stego/blob/main/specs/symbol-bindings.md),
including import names, constructor names, and fill aliases. The pin upgrade
preserved all 74 generated and dependency file hashes. Contract race tests and
the application build passed with the new pin.

The compiler also checks [constructor metadata](https://github.com/jsell-rh/stego/blob/main/specs/constructor-metadata.md)
before it generates startup code. Invalid middleware, dependency, and cleanup
indexes now cause a compiler error. This pin upgrade preserved all 74 generated
and dependency file hashes. Contract race tests and the application build passed.

[Identity reconciliation reads](acceptance/identity-queries.md) now use generated
bounded queries without unused totals. Current grants, retained user identity,
and controller-only access remain part of the same transaction.

The [generated gRPC client preserves absent stream headers](acceptance/grpc-stream-headers.md).
A real Gateway gate exposed an error-classification defect that the raw-client
test missed. The fix is in STEGO; the application now tests its generated client.

The compiler now records [registry input content](https://github.com/jsell-rh/stego/blob/main/specs/registry-snapshots.md)
in applied state and rejects registry changes after planning. This upgrade adds
the digest to state. All 73 generated and dependency files remain unchanged.
An independent digest calculation matched the saved value for all 11 local
registry inputs.

Database watch and replay now use [generated stream startup checks](acceptance/stream-startup.md).
STEGO owns header validation and early RPC error handling. Hypershell supplies
the capability names, replay scope, and controller error policy.

Service-account creation accepts [relative expiry](acceptance/service-account-cli.md),
such as `--expires-in 30d`. STEGO converts the duration to an absolute timestamp.
Hypershell retains lifetime limits and access rules in the API.

The generated [identity command](acceptance/cli-identity.md) reports the caller
accepted by the API. `whoami` supports OIDC refresh and protected token export.
Default output contains no token. The current-user extension is version 1.1.0
and adds verified issuer, subject, and access-token expiry fields.

The CLI `version` command returns separate application and compiler build records.
It works without login or API access. See [build records](acceptance/cli-version.md)
for the tested behavior and limits.

Identity recovery now resumes from retained grant IDs through the common STEGO
scan runtime and PostgreSQL checkpoints. Inventories above 10,000 references
continue across bounded passes and controller restarts.
See [identity cursor recovery](acceptance/identity-cursors.md).
The [scan-cycle record](acceptance/identity-cycles.md) also retains earlier action
failures after restart. Grant changes invalidate that evidence atomically.
The separate [grant condition](acceptance/grant-conditions.md) reports a complete,
clean scan and removes positive evidence when a grant changes.

Gateway identity now records a durable `ClientReady` condition through STEGO.
Configuration, condition, and event changes commit together. The recovery API
hides status from an older desired generation. See
[identity conditions](acceptance/identity-conditions.md) for scope and upgrade rules.

Generated state records the declaration, configuration, module files, and declared
protobuf inputs. See [project input records](acceptance/project-inputs.md) for the
manifest checks and their limits.

The database, Gateway workload, Gateway identity, and sandbox-count controllers
use STEGO-generated worker commands. Their main functions, signals, health
probes, safe process errors, and [controller metrics](acceptance/controller-metrics.md)
come from STEGO. Domain provider setup remains under `internal/`.
Set `STEGO_CONTROLLER_MONITOR_ADDR` to an available literal loopback address.
The default is `127.0.0.1:9081`. Each process in the same network namespace needs
its own port. The endpoint reports queue use, action outcomes, retries, and
duration without resource IDs or private error labels.

[Cleanup summaries](acceptance/cleanup-summaries.md) report pending deleted
resources and the oldest deletion time for an authorized owner and target.
The generated sampler runs independently of recovery scans and reports failed
reads as unavailable. Summary responses and metrics contain no resource IDs.

The [CNPG database workflow](acceptance/cnpg-database.md) uses the generated
controller runtime for the shared managed-database provider. It has a separate
real Kubernetes acceptance gate. The [CNPG Gateway workflow](acceptance/cnpg-gateway.md)
adds separate SQL identities, retained keys, application calls, and SQL-confirmed
cleanup on that shared Cluster. Its record states the test result and limits.

Generated [process failure records](acceptance/process-failure-privacy.md) report
the failed step without private database or component error text. A Gateway
failure and recovery test verifies rollback, safe output, and event delivery.

[HTTP diagnostics](acceptance/http-diagnostics.md) use fixed local and OTLP
events. A test-only panic route verifies safe logs, request signals, and continued
Gateway access without adding a fault route to the generated application.

[Task abort handling](acceptance/task-abort-handling.md) preserves peer cleanup
and safe failure output when a registered callback panics or exits without
return. The Gateway test verifies retained data through REST and gRPC after
process restart.

[Recovery sweep telemetry](acceptance/sweep-observability.md) supplies common
page and action logs, metrics, and spans for service-account recovery. The
application test verifies failed revocation and recovery after restart through
the generated runtime and a TLS OTLP collector.

[Outbound RPC signals](acceptance/rpc-client-observability.md) connect generated
client calls to the active request or recovery trace. Account recovery verifies
client logs, duration metrics, and child spans across failure and restart.

The [generated Go SDK](acceptance/go-sdk.md) uses the captured OpenAPI contract.
STEGO supplies its typed methods, HTTPS transport, and telemetry. Use the new
typed API and preserve HTTP contracts and behavior. Compatibility with the old
fluent SDK API is not required. Automatic pagination, TypeScript SDK, and web
console remain open.

The [generated Kubernetes service check](acceptance/kubernetes-service.md)
runs the Gateway workflow in separate API and identity worker Pods. STEGO
supplies their deployment resources, process behavior, health probes, and
telemetry. The test checks identity repair after worker and API Pod replacement.
The [worker abort check](acceptance/worker-run-abort.md) requires safe failure
output and identity recovery after a Run callback panic or `runtime.Goexit`.
