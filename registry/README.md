This registry composes common STEGO components for the Gateway acceptance work.
Component metadata comes from the revision in `.stego/compiler-revision`.
Output namespaces are public packages so application code and acceptance tests
can use generated models and runtime constructors. The service-core archetype
generates storage, authentication, an outbox, and one HTTP/event process.
The HTTP application factory is outside generated output. It selects verifier
mode and external migrations. Gateway response mapping and the query for the
earliest owner name remain domain code.

The compiler contains no Hypershell entity names or access rules. Those rules
are in `internal/gateways`. The declaration in `service.yaml` contains the
fields needed by this workflow. Cluster, database, release, user, and role
entities are partial models for this gate. They do not establish compatibility
for the other Hypershell workflows.

The `grpc-application` component compiles the pinned Gateway protobuf contracts.
Its domain factory is `internal/grpcapi`. The public `out/grpcapi/pb` packages
contain messages and client/server interfaces; the generated runtime owns TLS,
authentication, deadlines, and service shutdown.

The `tsl-search` component supplies declared-field search and common metadata
aliases. It has no custom field resolver in this variant. Search remains separate
from the domain access filter, which always applies before count and pagination.
