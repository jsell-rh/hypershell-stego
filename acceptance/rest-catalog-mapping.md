# REST catalog mapping candidate

Status: generated candidate. Application execution evidence is pending.
This branch does not establish application acceptance.

The service declares all 32 response properties for ManagedCluster,
GatewayRelease, and GatewayNetwork. STEGO generates their public OpenAPI types
and checked field conversions. Hypershell keeps catalog access, input rules,
pagination, field selection, and public error policy.

The adapters return conversion errors before the transport sends success. List
conversion stops on the first error. Optional values retain absent, empty, and
populated states. Reference kind, href, and timestamps remain present. An empty
reference ID retains its previous omission rule.

Acceptance tests use separate response types to check the HTTP contract. New
unit checks compare explicit JSON properties, pointer ownership, empty reference
values, and private conversion errors. A database check tests malformed stored
timestamps through get, list, field selection, pagination, access denial,
restart, and repair.

The acceptance checks below passed for this catalog change. Gateway and grant
REST mapping remain separate work.

Compiler `ca25ab06` passed exact-source checks and signature verification. A
separate installation matched all five verified release files. Regeneration run
`35567725658` produced 433 generated and module files. Review checked 424 output
hashes and 41 source-input hashes. Exactly two generated files were added; the
CLI compiler identity and three generation state files changed. The other 427
generated and module files stayed unchanged.

The complete model declarations match the existing SDK wire model declarations.
Source review covered all 32 mapping fields, timestamp checks, owned pointers,
and fixed errors. All ten hosted checks passed. API run `35570053583` passed 53
required roots. Browser run `35570745086` passed all 11 required roots. The
rendered workflow took 542 seconds. Review checked 1722 source files, 434
repeated generation hashes, and all four screenshots. Cleanup left the 32
standing installation resources unchanged.

The browser workflow proved login, Gateway creation, grants, REST/gRPC access,
events, restart, browser sessions, account lifecycle, correlated telemetry,
and namespace finalization. A cleanup reader had an outdated prior-API
assertion. Its correction checked the exact completed API proof and the same
cluster state; no workflow rerun was needed.

One measured Gateway had 100 service accounts. Complete cleanup had an observed
upper bound of 32.566 seconds, above the 30-second target. The 100-Gateway
capacity target is not proved. Live Kata, native Sandbox execution, RDS failover,
and the remaining enterprise requirements remain open. See the
[workflow evidence](rest-catalog-workflow-evidence.json).
