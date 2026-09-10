The compiler pin is `777d59184dd658ceb77b03d262caceaf0b4ae5c4`.
Telemetry component 1.5.0 adds a random `service.instance.id` to each generated
runtime. Its logs, metrics, and traces share that identity. Local JSON logs use
the same value. Hypershell adds no identity generator or exporter configuration.

`TestGatewayTelemetrySeparatesReplicasAndRestart` starts two generated API
processes with the same service name and PostgreSQL database. It creates a
Gateway through REST on the first process, checks its committed owner grant,
and reads it through gRPC on the second process. The second process must deny
a request without credentials. The generated runtime must deliver the Gateway
event. The test restarts the first process while the second remains active and
reads the retained Gateway through REST.

The collector must receive three distinct runtime identities. Each instance
must have exactly one start and stop event, and one HTTP duration sample. Only
the second instance has a gRPC duration sample. All four request logs, spans,
and metric exemplars must belong to the correct instance. Local lifecycle logs
must match the exported identity. Metric point labels must not contain the
instance ID. Exported data must exclude Gateway names, IDs, and credentials.

The baseline at compiler `86b436c` completed the application actions but failed
in 5.69 seconds because exported resources contained only the shared service
name. The pinned implementation passed the full replica workflow in 5.00 seconds
under race detection. A separate controller test confirms that API restart does
not change the identity of a controller that remains running.

All six selected acceptance workflows passed with PostgreSQL required in
52.917 seconds. They cover replicas, API restart, controller failure and recovery,
HTTP and gRPC tracing, combined request telemetry, collector loss, and local
logging without export. Internal and contract race tests and static checks
passed. Full Keycloak, Kubernetes, and VM provider gates were not repeated for
this identity-only change. New remote CI results remain separate evidence.

Repeat generation preserved all 97 output, state, and dependency hashes. The
new generated file is `out/tracing/identity.go`. No dependency version changed.
IDs are created at runtime and are not written into generated source or state.

The [compiler contract](https://github.com/jsell-rh/stego/blob/777d59184dd658ceb77b03d262caceaf0b4ae5c4/specs/telemetry-instance-identity.md)
defines UUID creation, lifetime, failure handling, and field ownership. A new
runtime gets a new ID, including within one process. Overlapping controllers
that share providers also share their identity. Export queues and shutdown
limits are unchanged. Backend storage and aggregation must preserve resource
identity. Distributed controller ownership, full process logging, non-keyed
telemetry, database and outbound spans, and production capacity remain open.
