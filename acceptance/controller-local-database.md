# Controller-local Gateway databases

## Decision

On 2026-09-14, the user removed `database_id` from the target API and referred to
[Hypershell PR 272](https://github.com/openshift-online/hypershell/pull/272).
The reviewed PR revision is `d61f1dffe639f1de6e67cd82d89282fedb0d15e6`.
That PR contains specifications, not an implemented release.

The target has no Gateway database-selection field, empty ID placeholder,
`ManagedDatabase` API entity, database registration call, or central database
mapping. `ManagedCluster` and its registration remain supported. A Gateway
request selects its execution cluster. The API commits the Gateway, owner grant,
and events without querying a database catalog.

The installation supplies a PostgreSQL server and a local administrative Secret
reference to each execution controller. Terraform can supply RDS before cluster
creation. Installation GitOps can supply CNPG. Both use the same SQL connection
contract. The application controller creates one logical database and login per
assigned Gateway. It does not create or delete PostgreSQL servers, CNPG Cluster
resources, or server volumes.

## Required behavior

- Remove database-selection fields from REST, gRPC, SDKs, CLI commands, console
  workflows, events, and active storage models. Reserve retired protobuf numbers
  and names. Use matching release clients in the existing API namespaces.
- Reject retired request fields. An empty `database_id` is not a supported
  compatibility input. No database registration step may precede creation.
- Read only the declared local Secret. Use verified TLS, bounded calls, private
  errors, and least-privilege SQL access. Administrative credentials must not
  reach Gateway Pods or public interfaces.
- Preserve assigned-controller ownership through lists, watches, retries,
  restart, and deletion. A caller-supplied cluster ID does not grant authority.
- Preserve data and credentials on repeat reconciliation. Record the database
  destination durably. Reject an unsupported cluster or destination change
  before SQL, Secret, or workload mutation.
- Retain cleanup intent and the original destination until SQL cleanup succeeds.
  Delete only the Gateway's SQL objects and credentials. Keep the supplied
  server, its storage, and unrelated databases.
- Supply a fresh schema with no database catalog or Gateway database reference.
  Reject legacy and unknown schemas before schema or data writes. Serialize
  bootstrap and permit safe retry after interrupted initialization.
- Require matching API and controller generations before reconciliation.
  Deployment inspection failure must stop deployment. Existing installations
  remain unchanged; teardown requires a separate explicit operator action.

STEGO supplies SQL lifecycle, secure clients, bounded reconciliation, telemetry,
and common schema/bootstrap checks. Hypershell supplies assignment, Gateway
identity, cleanup order, and its release compatibility policy. No common STEGO
component may depend on a Hypershell entity name.

## Acceptance and current state

The source now removes the field from requests, responses, storage, SDK inputs,
and the console. The database catalog and server resource controller are removed.
The replacement workload controller uses STEGO's SQL lifecycle. The
[real browser workflow](controller-local-test-transition.md#supplied-server-browser-result)
now passes with a supplied PostgreSQL server. The full application gate has not
passed on this model. The earlier registration and CNPG resource
tests are historical evidence; they do not establish this new contract.

The next application gate must create a Gateway without a database field or
database seed. It must prove atomic ownership, REST and gRPC reads, filtered
lists, denied requests, generated event delivery, restart, and regeneration.
The assigned controller must then prove real SQL creation, isolated logins,
partial-creation recovery, stable destination checks, and durable deletion.
Run the same controller behavior with installation-supplied PostgreSQL and CNPG.
Actual RDS checks remain required for RDS permission and connection claims.

The schema gate must cover fresh bootstrap, concurrent starts, interrupted
bootstrap, empty and populated legacy schemas, unknown generations, and an old
process that can still use its schema after a new process rejects it. No test
may treat an inspection failure as an empty installation.

The uncommitted registration-test update was withdrawn after this decision.
Its bounded Job `stego-ci/gateway-api-3d59696588b6` failed compilation before any
test ran. The optional gRPC cluster ID assertion had a type mismatch. The Job,
Pods, Secrets, and ConfigMap were removed, and the shared Lease was released.
The failed evidence remains in `/tmp/hypershell-registered-placement-v1`.
It is not a passing gate and will not be used to justify the retired model.

## Request contract removal

The first code change removes `database_id` from Gateway create and patch
requests. The Go and TypeScript SDKs, CLI, and console use the new request
shape. REST rejects the retired property, including empty and null values.
gRPC reserves the old field numbers and names and rejects unknown request
fields. The captured upstream contracts remain unchanged; the application
contracts declare this breaking change.

The bounded jshell check passed all 17 required Gateway tests under race
detection. It checks REST and gRPC rejection, valid creation, ownership,
filtered access, SDK and CLI calls, event delivery, and restart. The rejection
test records every event insert so delivery cannot hide an unwanted event.
Console type, architecture, lint, and UI checks passed. Repeated generation
produced the same files before and after the checks. The
[request evidence](gateway-request-contract-evidence.json) records source
hashes, failed attempts, limits, and cleanup.

At the request-only revision, responses and storage still had the field, and
selected tests seeded a database record. That result does not prove creation
without a database catalog.

## Model and controller transition

The active OpenAPI documents are separate from the captured reference. Gateway
response field 6 and its `database_id` name are reserved in protobuf. The active
service has no `ManagedDatabase` entity, API methods, or registration commands.
The Gateway transaction checks only its cluster and release. Cluster changes
return a conflict because a normal patch cannot migrate stored Gateway data.

The installation supplies `hypershell-gateway-workload-files`. Set
`HYPERSHELL_GATEWAY_DATABASE_CONFIG_FILE` to the absolute path of its JSON file
under `/var/run/stego`. The file contains `host`, `port`, `database`, `user`,
`password`, and `ca` (PEM certificates). Its permissions must deny access by
other users. Each attempt reads the current projected file. The SQL administrator
must have the permissions required by STEGO's PostgreSQL provisioning contract.
The provisioning database contains STEGO's private ledger. It is separate from
Gateway application databases.

A Gateway state namespace retains its signing keys, encryption key, SQL password,
and server binding. An immutable public record and a namespace annotation pin
that state before SQL provisioning can start. The namespace permits no Pods or
persistent volumes. A workload namespace can be replaced without replacing the
source keys. The controller records SQL cleanup and workload cleanup separately.
It records SQL cleanup first. The allocator removes source state only after both
records show completion. Later workload checks can reopen workload cleanup
without the SQL credentials. Give the controller exact `Gateway` grants for
`cleanup.sql` and `cleanup.workload`, each with its assigned cluster as target.
Missing recorded credentials or a changed destination stops the operation.

STEGO supplies deterministic SQL names from the full installation and resource
identity. These names retain case-sensitive ID distinctions. They differ from
the draft PR's lowercased Gateway names. The controller publishes only the
Gateway login, encoded URI, and TLS trust to the Gateway workload. It never
publishes the SQL administrator's credentials.

The fresh schema generation is `controller-local-v1`. The generated API startup
runs entity, outbox, and role setup in the same guarded bootstrap transaction.
A recognized generation does not repeat setup. Old or unknown schema state is
rejected before application writes. Historical migration files remain unchanged.

The application test conversion is incomplete. Three old live workload and
recovery files still refer to removed types. The full
`generate.sh` dependency check and acceptance suite remain required. A limited
production build or state unit test is not the application gate. The real
supplied-server browser workflow now checks SQL isolation, generated worker
access, restart, and normal deletion. Cleanup before initial state creation,
the remaining state-loss and SQL fault cases, database-server restart,
installation CNPG, and actual RDS checks remain required before release.

## Focused application check

The initial conversion used `controller-local-api.files` to select tests while
older provider fixtures still imported removed packages. That temporary list is
retired and remains in Git history. The complete acceptance package now builds.
Use the workflows in the [acceptance index](README.md). The current restricted
API gate requires 30 named checks and compiles the complete acceptance package.
Its separate browser workflow proves the real Gateway workload and SQL lifecycle.
See [the current CI evidence](browser-ci.md) for source revisions and limits.

## Verified API result

The bounded jshell Job `controller-local-c22019941c11` passed all 21 selected
application tests and 59 top-level tests in total under the race detector. It
passed the production Go build and two identical generation runs. It used
STEGO `c71878eebe67e488dea9dd199815df023d11ffb5`, whose
[full CI passed](https://github.com/jsell-rh/stego/actions/runs/34909222364).
The previous Job passed console typecheck and lint on the same console source
and bundle. Its three API fixture failures were corrected before the repeat.

The API result covers creation without a database catalog, atomic owner grants,
rollback, filtered lists, denied requests, REST/gRPC reads, watch failure,
durable event delivery, restart, retired-field rejection, and separate SQL
cleanup grants and versions. Legacy-schema checks preserve rows and an existing
prepared SQL statement. They do not run an old API binary.

[Recorded evidence](controller-local-api-evidence.json) includes test names,
frozen input hashes, generated output hashes, limits, prior failure scope, and
cleanup results. The Job, Pods, and private fixtures are absent. The shared test
Lease was released. The full acceptance conversion and live SQL/workload gate
remain open. In particular, cleanup before initial source state exists still
needs a complete application test and implementation review.

The [expanded test result](controller-local-test-transition.md) adds passing
SDK, CLI, catalog, identity, telemetry, cleanup deadline, cleanup backlog, and
parent-deletion checks. All 81 selected application tests have passing results
across the full run and its bounded correction run. The full acceptance build
and live SQL/workload gate remain open.
