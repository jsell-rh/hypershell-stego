This registry composes common STEGO components for the Gateway acceptance work.
Component metadata comes from the revision in `.stego/compiler-revision`.
Output namespaces are public packages so application code and acceptance tests
can use generated models and runtime constructors. The service-core archetype
generates storage, authentication, an outbox, and a Kafka event process.

The compiler contains no Hypershell entity names or access rules. Those rules
are in `internal/gateways`. The declaration in `service.yaml` contains the
fields needed by this workflow. Cluster, database, release, user, and role
entities are partial models for this gate. They do not establish compatibility
for the other Hypershell workflows.
