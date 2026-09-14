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

Gateway requests no longer accept `database_id`. Responses, storage, and the
controller still use database registration and the old field. The implementation
does not yet satisfy the complete decision. The earlier registration and CNPG resource
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

This is an intermediate code change. Gateway responses and storage still have
the field. The catalog, registration calls, and old controller still exist.
The selected tests still seed a database record. They do not prove the required
creation workflow without a database catalog. The next change must remove
those dependencies and connect the assigned controller to STEGO's SQL lifecycle.
