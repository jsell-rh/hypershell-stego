# Generated Go SDK workflow

STEGO generates `out/sdk` from the nine captured reference OpenAPI files. The
variant selects `go-sdk` and declares these inputs. It contains no SDK transport,
retry loop, serializer, or telemetry implementation.

The public SDK uses typed methods with an explicit context. Create a client with
`sdk.NewClient(sdk.Options{BaseURL: endpoint, Token: token, CAFile: caFile})`.
Call `Close` when the client is no longer needed. Inspect the returned error and
HTTP status. For example, `GetGatewayWithResponse` exposes `JSON200` and
`JSON404`; a denied resource read has status 404.

`TestGeneratedGoSDKGatewayWorkflow` uses the generated SDK against the generated
application with PostgreSQL, a TLS API endpoint, a Kafka protocol fixture, and a
TLS OTLP collector. It checks:

- Typed creation preserves the DNS array and returns the assigned KSUID,
  namespace, database ID, and timestamps.
- Creation commits the Gateway and its exact owner grant, then delivers its event.
- An owner can read without the creator role. Another user receives a typed 404.
  Filtered lists have the correct rows and totals for both users.
- A forced event-write failure rolls back the Gateway, grant, and database.
- SDK and TLS gRPC reads agree before and after API restart.
- Typed update and deletion work. The deletion event is delivered.
- The SDK produces correlated CLIENT spans, completion logs, and duration metrics.
  The API SERVER span has the SDK span as its parent. Exported signals exclude
  tokens, IDs, names, DNS entries, database credentials, and SQL constraint text.
- Loss of the SDK collector does not prevent a read or bounded client shutdown.

The shared compiler tests cover a separate Widget API with and without telemetry,
verified HTTPS, custom trust roots, path prefixes, redirect rejection, response
limits, malformed JSON, safe errors, deadlines, and client shutdown. Input checks
cover captured references, duplicate keys, unsupported extensions, recursive
schemas, imported parameter types, name conflicts, and repeat generation.

The Go API is new. It does not yet supply the old SDK's fluent builders, resource
groups, or automatic list iterator. It does not include the separate current-user
contract extension. TypeScript SDK and browser work remain open. This workflow
is not a production capacity test.

## Acceptance result

On 2026-09-11, compiler
`b6fc0ae17ce726a0ade732af1eee7477d94002db` generated the SDK in a bounded jshell
job with Go 1.26.8 and PostgreSQL 18.6. The test container had a one-CPU limit and
3 GiB memory limit. No local build or performance test was used.

Eight selected Gateway tests passed with race detection in 26.657 seconds. The
SDK workflow passed in 5.85 seconds. The group also covered REST/gRPC behavior,
atomic owner and event writes, rollback, access filtering, event delivery after
restart, and bounded database connections. Contract tests and static checks
passed. Internal race tests, module verification, and both compiler and
application vulnerability scans also passed on the candidate source. Both scans
reported no vulnerabilities.

Two fresh builds of the pinned compiler produced the same 106 output, state,
and dependency hashes. The hashes also matched after the tests and after the
files were copied to the local checkout. The captured application source was
`50757ed08dfd42b3174a4ef37d4290ca1f26d4b00890c69d7a7384a6960340bf`.
Full CI is a separate result. The earlier current-user concurrency failure is
recorded in [CI evidence](ci-evidence.md) and remains open.

## Nullable fields and service accounts

SDK component version 2 preserves omitted fields, explicit null, and values.
The version 1 SDK lost explicit null in an optional account description.
`TestGeneratedSDKPreservesNullableAccountFields` reproduced that error against
the unchanged generated SDK before the compiler correction. The test also
checks empty and nonempty strings.

Nullable fields now use `nullable.Nullable[T]`. For example, use
`input.Description.SetNull()` to send an explicit null,
`input.Description.Set("")` to send an empty string, and
`input.Description.SetUnspecified()` to omit the property. Use `IsSpecified`,
`IsNull`, and `Get` to inspect response fields. This changes the Go source API
for nullable properties. The reference OpenAPI files are unchanged. STEGO
generates the types and supplies the dependency. The variant has no custom
nullable-field serializer.

`TestGeneratedSDKServiceAccountWorkflow` uses the generated client, a TLS API
endpoint, PostgreSQL, and the existing authenticated gRPC provisioner fixture.
It checks the JSON request received at the API boundary for all four description
cases. The API returns the stored account ID, its Gateway ID, and a creation
credential. Later get and list responses contain no client secret. Null error
and revocation fields retain their state. Stored descriptions survive a process
restart. An unrelated user receives a typed 404. Revocation returns a timestamp,
and deletion succeeds. The provider is a protocol fixture; this check does not
replace the separate real Keycloak workflow.

The corrected compiler is `c06b9510971c68af64eaa2744b506a13e5bb0955`.
Its [full CI run](https://github.com/jsell-rh/stego/actions/runs/34610251961)
passed. In jshell, two fresh builds of that commit produced identical hashes
for all 106 generated, state, and dependency files. The hashes also matched
after the tests and after the files were copied to the local checkout.

The four selected race tests passed against the pinned output in 19.826 seconds.
The new SDK account workflow took 5.38 seconds; the SDK Gateway workflow took
6.15 seconds; the existing service-account runtime workflow took 7.24 seconds.
Contract tests passed in 1.558 seconds on the preceding candidate output.
Acceptance and SDK static checks, module verification, and the application
vulnerability scan passed. The scan reported no vulnerabilities.

Job `nullable` used namespace `stego-nullable-20260911`, Go 1.26.8, and
PostgreSQL 18.6. The test container had one CPU and a 3 GiB memory limit. The
database had half a CPU and a 512 MiB memory limit. The job deadline was
30 minutes. The final pinned application archive SHA-256 was
`13bb5a4fcf2f37f30a17f383c378cf389b031303a57c4b9849036aca16cd34ba`.
Final local edits changed evidence documents only. No local build or performance
test was used. Full application CI for this revision is a separate result.
