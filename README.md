This repository is the test bed for a STEGO-based Hypershell variant.

The current account integration uses STEGO compiler `3e0bc22401729eb95bc1bd304455799a6d378b65`
and Keycloak provider `0.13.0`. The common lifecycle replaces the application's
administrator HTTP client and token cache. Hypershell retains Gateway roles,
account quotas, expiry, and authorization policy.

| Current qualification | Result and scope |
| --- | --- |
| Account browser workflow | [Passed on `a570dd0`](https://github.com/jsell-rh/hypershell-stego/actions/runs/35047697086/job/104641062793): real login, SQL journal, credential delivery, tokens, revoke, and delete |
| Core, regeneration, web console, and images | [Passed on `a570dd0`](https://github.com/jsell-rh/hypershell-stego/actions/runs/35047697086); core acceptance took 1382.383 seconds, including corrected orphan cleanup |
| Full Gateway cluster workflow | [Passed on `048ff55`](acceptance/common-account-lifecycle-20260916.md), including the common account lifecycle, provisioner replacement, real Gateway credentials, and verified cleanup |
| Private account state API and restart | [Passed on jshell](acceptance/service-account-provider-state.md): all 34 required API tests and cleanup passed |
| CNPG | [Passed on `a570dd0`](acceptance/common-account-lifecycle-20260916.md): common account lifecycle, database primary replacement, 24 network checks, and confirmed cleanup |

These results do not establish production capacity or complete application parity.
Use one provisioner replica with `Recreate`, stable journal keys, and the exact
private API grant. Stop old writers before migration. Cross-process writer
fencing and detection of a whole-database rollback remain open. See the
[provider boundary and results](acceptance/keycloak.md).

[Gateway namespace network isolation](acceptance/gateway-network-isolation.md)
is enabled for Gateway and state allocations. The declaration permits fixed
operator-approved Pod peers and Kubernetes addresses. An earlier complete
workflow passed 28 fresh-connection checks before and after recovery. External
DNS tracking and arbitrary external database destinations still need separate
implementation and qualification; passing the account workflow does not prove
them.
The [complete public Gateway workflow](acceptance/public-gateway-complete-20260915.json)
passed through the generated browser backend, REST, gRPC, restart, regeneration,
public TLS, certificate rotation, service accounts, telemetry, and normal cleanup.
Its linked source and configuration define its scope. Use the separate network
record for traffic-enforcement evidence.

The [controller-local database change](acceptance/controller-local-database.md)
removes `database_id`, `ManagedDatabase`, and database registration from the
application. Controllers use an installation-supplied PostgreSQL server.
The [supplied-server browser workflow](acceptance/controller-local-test-transition.md)
now passes with real Gateways, isolated SQL logins, REST and gRPC, events,
restart, and deletion. Earlier database registration and provider results
do not prove this model. The full application gate remains open. This transition
requires matching releases and a fresh schema. Existing installations require
explicit teardown and recreation; the application does not perform that action.

The [complete CNPG workflow](acceptance/cnpg-complete-evidence.json) also passes
with database primary replacement, namespace recovery, access checks, account
cleanup, and automatic test cleanup. The later
[unattended CNPG workflow](acceptance/cnpg-ci-complete-20260915.json) also passed.
Restricted CI created the disposable server, ran the complete application test,
and removed runtime resources, private fixtures, claims, and volumes. The static
operator installation remained.

STEGO must provide common service infrastructure and generated contracts.
Hypershell must supply its unique business rules and application workflows
through explicit extension points. No STEGO component may depend on a Hypershell
entity name or application rule.

The browser workload test uses STEGO's existing allocation model for
[restricted inspection](acceptance/browser-inspection.md). Its complete workflow
passes with named Secret reads and namespace permissions. The fixture adds no
production worker rights. The public and unattended CNPG records above contain
the later complete workload results and their limits.

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
| Control plane | Gateway identity, workload, and SQL lifecycle; cluster, network, and release operations |
| Clients | SDKs, CLI commands, web workflows, and authentication |
| Operations | Health, readiness, metrics, tracing, limits, shutdown, and deployment checks |

Compiler correctness and reusable infrastructure changes belong in STEGO.
Behavioral acceptance tests and Hypershell domain code belong here. Passing
compilation alone does not establish compatibility or production readiness.

Run `scripts/generate.sh` to regenerate with the pinned STEGO compiler. Run
`scripts/generate.sh --check` from a clean checkout to check committed output.
The [compiler preflight checks](acceptance/compiler-preflight.md) reject invalid
component inputs before rendering and retain the checked source snapshots.

Run `go test ./contracts` in CI to verify the source hashes, validate OpenAPI references, and
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
startup initializes only a fresh schema under the generation guard. It rejects
old or unknown schema state before writes. The REST list supports `page`, `size`, `search`, and `orderBy`, including a
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
`namespace`. A normal patch cannot change the execution cluster. Patches cannot set `active_sandbox_count`.
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
The retired database catalog has no patch or observation API. Gateway SQL
cleanup requires the `Gateway` / `cleanup.sql` grant for the assigned
ManagedCluster ID. The workload controller uses its installation-supplied
PostgreSQL credentials for SQL operations. Those credentials do not grant API
access. See the [current database contract](acceptance/controller-local-database.md).

Gateway deletion removes related provider clients before it commits the Gateway,
account metadata, cleanup audits, and deletion event. The Gateway row lock
prevents concurrent account creation from escaping cleanup. Provider failure
returns HTTP 503 or gRPC `Unavailable` and keeps the Gateway. See the
[Gateway account cleanup workflow](acceptance/gateway-account-cleanup.md).

The gRPC `AdjustActiveSandboxCount`, `SetActiveSandboxCount`, and private
`SetObservedSandboxCount` methods use STEGO's resource-locking transaction.
They require a configured control-plane subject and a `Gateway` /
`observe.sandbox-count` grant for the locked Gateway's ManagedCluster ID in
`HYPERSHELL_CONTROLLER_WRITE_GRANTS`. Other controller grants do not permit
count changes. Owners, viewers, creators, and admins have no implicit access.
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
bindings. The current implementation moves native-client lifecycle and protected
recovery state into STEGO. Application validation of this migration remains open;
see the [identity workflow](acceptance/gateway-identity.md).

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
and credential readiness checks reject stale observations. The former database
catalog's [observation](acceptance/database-observations.md) and
[cleanup](acceptance/database-cleanup.md) results are historical records.
The current workload controller retains each Gateway's SQL destination and
cleanup intent. A failed SQL cleanup preserves its source credentials and
pending state. See the [supplied-server workflow](acceptance/controller-local-test-transition.md).
Gateway login identity uses [durable cleanup observations](acceptance/gateway-identity-cleanup.md).
The controller confirms provider absence and checks again after completion.
Workload cleanup now [retains each cluster target](acceptance/gateway-target-cleanup.md).
A former cluster can complete its own cleanup without completing another cluster.
Complete production acceptance remains open.
These correctness requirements take priority over recovery-query optimization.

[Persistent user identity](acceptance/user-identity.md) now uses the verified
issuer and subject. Username changes preserve grants; username reuse cannot
transfer them. This release requires a fresh schema. It does not migrate legacy
users or preserve an old installation through an in-place upgrade.

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

The active placement catalog contains clusters, releases, and networks. Gateway
creation does not require a database record. The controller reads its declared
PostgreSQL configuration and uses STEGO to create a separate SQL database and
login for the Gateway. The installation owns the PostgreSQL server.

The former deployment and CNPG resource workflows are historical evidence.
Their tests and setup scripts still need conversion to the controller-local
contract. They do not prove the new workflow. Do not use their database seed,
`DATABASE_PROVIDER`, or legacy migration instructions for this release.

The historical [Gateway workload gate](acceptance/gateway-workload.md) used the
retired server controller. Its setup script does not support this release.
The [converted browser workflow](acceptance/controller-local-test-transition.md)
uses the actual OpenShell Gateway, Keycloak, and an installation-supplied
PostgreSQL server. The transition record separates current results from checks
that still need conversion.

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

The historical [catalog CLI workflow](acceptance/catalog-cli.md) used database
provider selection. The converted CLI tests create clusters, releases, networks,
and Gateways without a database catalog. They check protected deletion and event
rollback. Their current results are in the transition record.

The [Gateway-network workflow](acceptance/gateway-networks.md) now checks network
record CRUD, access, CLI commands, watch events, rollback, restart, and database
upgrade. It preserves the reference metadata behavior. Neither this port nor
the reference network controller sets up tunnels. The complete Hypershell port
and production acceptance remain open.

The [generated apply workflow](acceptance/cli-apply.md) now creates and patches
catalogs and Gateways from resource documents. It checks dry runs without API
contact, complete preflight, partial failures, access, events, and restart.
The converted tests use installation-supplied SQL. [RoleBinding apply](acceptance/cli-immutable-apply.md) also creates
grants and recognizes an exact existing match without a write. The common
runtime is supplied by STEGO. Kustomize rendering and the complete CLI port
remain open.
Recovery now uses [generated storage cursors](acceptance/storage-cursors.md) for
Gateway IDs and service accounts. These queries preserve
access and state filters without unused counts or application-built ID searches.

Controllers use [generated observation budgets](acceptance/observation-deadlines.md)
to leave time for a conditional status or cleanup write after a provider timeout.
Five workflows check failure, event delivery, API restart, and recovery.

The historical [database recovery record](acceptance/database-recovery.md)
describes the retired database catalog. The current Gateway controller retains
SQL cleanup work on the Gateway and checks its assigned controller scope.

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

The historical database watch test established [generated stream startup checks](acceptance/stream-startup.md).
STEGO owns header validation and early RPC error handling. The database watch
API is retired. Current Gateway controllers use the generated stream runtime.

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

The namespace allocator, Gateway workload, Gateway identity, and sandbox-count
controllers use STEGO-generated worker commands. Their main functions, signals, health
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

The historical [CNPG database workflow](acceptance/cnpg-database.md) and
[CNPG Gateway workflow](acceptance/cnpg-gateway.md) tested the retired server
controller. The current installation owns CNPG or RDS infrastructure. Its
Gateway controller manages logical SQL resources through STEGO. The
[supplied CNPG workflow](acceptance/cnpg-installation.md) passed its application
checks. Its final cleanup read failed; separate checks confirmed cleanup.
The later unattended CNPG result linked above supersedes this cleanup result;
it does not qualify the current account migration.

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
fluent SDK API is not required. Automatic pagination and the complete web console remain open. The generated
TypeScript client has a separate protocol check below.

The [generated Kubernetes service check](acceptance/kubernetes-service.md)
runs the Gateway workflow in separate API and identity worker Pods. STEGO
supplies their deployment resources, process behavior, health probes, and
telemetry. The test checks identity repair after worker and API Pod replacement.
The [worker abort check](acceptance/worker-run-abort.md) requires safe failure
output and identity recovery after a Run callback panic or `runtime.Goexit`.

The [browser Gateway protocol check](acceptance/browser-gateway.md) uses a
separate generated Go console backend. STEGO owns login, sessions, token renewal,
logout, and the API proxy. Hypershell declares routes, assets, roles, and the API
prefix. The backend now serves the captured React console.

The [browser session compatibility check](acceptance/browser-session-compatibility.md)
checks the sign-in recovery response and sign-out through the generated console
and Keycloak. A confirmation form protects the reference UI's GET sign-out link.

The [TypeScript browser client check](acceptance/browser-typescript.md) uses the
STEGO-generated module with the real Gateway workflow. STEGO supplies the
transport and runtime checks. The rendered Gateway workflow now uses this client.

The [React console source port](acceptance/web-console-port.md) now uses the
STEGO browser SDK and telemetry runtime. Its 229 domain and UI tests, type checks, lint, and production
build pass. The generated backend now serves the captured build. Three rendered Gateway
checks passed with common telemetry, collector failure, access checks, and
API and backend restart. See the linked record for the scope and remaining gates.

The [rendered service-account check](acceptance/browser-service-accounts.md)
uses the console and real Keycloak to check one-time credential delivery, token
claims, reload, revocation, and deletion. It uses a Gateway readiness fixture.
This readiness fixture does not prove workload provisioning. The separate full
Gateway workflow linked above supplies that evidence for its recorded revision.

The [RPC process check](acceptance/rpc-process.md) replaces the handwritten
provisioner entry point with STEGO output. Hypershell keeps Gateway policy and
the allowed callers. STEGO supplies the common Keycloak provider, authentication,
telemetry, signals, and cleanup.
The [RPC deployment check](acceptance/rpc-deployment.md) adds a generated
provisioner Deployment and replaces its Pod during the rendered account workflow.

The [browser Gateway workload check](acceptance/browser-gateway-workload.md)
uses the actual console-created Gateway. Generated workers provision its
PostgreSQL database, identity client, and OpenShell Deployment. Verified RPC,
denied calls, provider data after Pod replacement, and a real browser-issued
service credential passed in the bounded jshell profile.

The [worker deployment check](acceptance/worker-deployment.md) runs all three
controllers as separate generated Deployments. It verifies their Kubernetes
identity projections, declared RBAC, Pod replacement, and metrics with
correlated logs and traces. The complete browser Gateway and account workflow
passed with these workers. Shared-cluster isolation remains open.

Gateway SQL registration requires the exact `configure.sql` controller grant
for its managed cluster. The worker registers the retained state digest before
SQL provisioning. Cleanup requires the exact `cleanup.sql` grant. Only a deleted
Gateway can close registration. Closed registrations and SQL deletion records
must be retained. This release uses schema generation `controller-local-v2`;
it rejects earlier generations before startup and does not migrate old data.


The [SQL registration browser result](acceptance/sql-registration-browser-evidence.json)
proves the complete-package Gateway workflow, early deletion before workers
start, SQL fault recovery, PostgreSQL restart, and retained installation data.
All 229 generated files match repeat generation. The broader installation,
Sandbox, and SQL session-isolation requirements remain open.

The service-account provisioner now uses STEGO's common Keycloak lifecycle and
[protected recovery journal](acceptance/service-account-provider-state.md).
Common code manages creation, migration, access repair, and retained cleanup.
Hypershell supplies Gateway ownership and OpenShell role policy. This migration
requires stable journal keys and an exact API grant. Its rendered browser
and full cluster workflows passed on commit `048ff55`. The private API and
restart gate also passed on jshell. The complete core suite and CNPG workflow
passed on `a570dd0`. The [result record](acceptance/common-account-lifecycle-20260916.md)
states the tested scope and the remaining limits.
