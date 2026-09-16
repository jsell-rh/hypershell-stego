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

The first jshell API run, [35037984214](https://github.com/jsell-rh/hypershell-stego/actions/runs/35037984214),
failed at the event assertion. The test counted the whole pending queue after
access checks. Request preparation can create valid global-role grant events
for those checks. The test now captures the event sequence after access checks
and compares it across state operations. This also detects an event that the
runtime has already delivered. A new runtime run is required for the remaining
restart and cleanup assertions. The other required API tests passed in that run.

Compiler `aa75def93e5673578c165a3fbd78ea7b243d0395` adds the common
`StateJournal`. The Gateway adapter fixes the identity scope, resource ID,
observed revision, and cleanup mode. It rejects a mismatched response before the
journal can return usable state. STEGO supplies encryption, bounds, version
checks, and exact save-response checks. The adapter contains no encryption or
write-retry mechanism.

The acceptance test now loads the largest retained record through this journal
after API restart, saves the next protected version through TLS gRPC, and rejects
both a stale state snapshot and a stale Gateway observation. Local compilation
passed. Runtime results for this extension remain pending. The production
Keycloak controller still needs the common provider lifecycle and key setup.
