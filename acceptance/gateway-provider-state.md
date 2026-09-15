# Gateway provider recovery state

The private identity controller API stores one protected record for each Gateway.
STEGO supplies the versioned SQL store and the encryption codec. Hypershell fixes
the scope to `identity-provider` and applies its controller permissions.

A live write requires `configure.identity`. A cleanup write requires the exact
Gateway identity cleanup grant and a retained deletion record. Both writes require
the observed Gateway revision and the prior record version. An owner grant or a
platform administrator role does not give access to this record.

The controller keeps the encryption key outside the API and its database. The API
checks envelope format and a 60 KiB bound. This leaves space for resource metadata
within the generated 64 KiB RPC limit. A format check cannot authenticate a record
or prove that its contents are encrypted. The controller must decrypt and
validate every record before use. Encryption binds the instance, Gateway, scope,
and record version. It does not detect restoration of the whole database to an
older valid snapshot.

State writes do not change Gateway revisions or emit domain events. Deletion
retains the record so that cleanup can use the saved provider IDs. The API does
not expose a delete operation for these records.

`TestGatewayProviderStateAcrossGRPCAndRestart` creates a Gateway through REST and
uses the generated TLS gRPC runtime to save and read recovery state. It checks
controller permissions, resource revisions, stale record versions, size bounds,
ciphertext in PostgreSQL, API restart, and retained cleanup. It also checks that
the largest permitted record fits the generated request and response bounds.
The core CI suite and the required jshell API gate include this test.

The first local check compiles the test only. Runtime results must come from CI
or jshell. The production Keycloak controller does not yet use this API. The
common provider journal and lifecycle must use it before ownership migration
and full client lifecycle adoption can proceed.
