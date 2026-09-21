# REST catalog mapping candidate

Status: source preparation. Generated output and execution evidence are pending.
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

Before acceptance, verify the signed compiler release, regenerate all modules in
CI, retain the existing checks, and run the complete Gateway workflow. Gateway
and grant REST mapping remain separate work. No test pass is claimed here.
