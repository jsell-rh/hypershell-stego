The database, Gateway workload, Gateway identity, and sandbox-count controllers
now use STEGO's optional keyed-controller metrics. The compiler pin is
`b24ac6e7877978988d3df075b4661e6970401c5c`, with controller component 1.9.0.
Hypershell supplies the address and collector connection. STEGO supplies metric
storage, queue measurements, output, listener checks, and process shutdown.

Set `HYPERSHELL_METRICS_ADDR` when starting a controller to enable the endpoint:

```sh
HYPERSHELL_METRICS_ADDR=127.0.0.1:9090 ./database-controller
curl --fail http://127.0.0.1:9090/metrics
```

Keep the controller's normal API and provider settings. Each controller process
needs an available port in its network namespace. An absent or empty metrics
address starts no listener and allocates no collector. The address must use a
literal loopback IP and a port from 1 through 65535. Public addresses, wildcard
addresses, hostnames, and interface zones are rejected. A port already in use
prevents the controller from starting.

The endpoint returns Prometheus text with the `stego_controller_` prefix. It
reports queue capacity, queued work, work held by workers, queued retries,
admission waiters, source readiness, completed action outcomes, scheduled retries,
completed scans, watch reconnects, and an action-duration histogram. Metrics have
fixed names and labels. Resource IDs, tokens, provider errors, and other domain
values are not collected. The generated runtime retains counters across watch
reconnects. Process restart resets them. Queue gauges reset when the queue stops.

A retry gauge includes delayed retries and retries whose delay has elapsed.
Readiness means that the source permits new actions; it is not proof that every
resource is current. A completed action can still be followed by later drift.
The counters do not replace durable resource conditions or pending cleanup data.
Loopback collection assumes trusted access inside the host or network namespace.
Remote collection requires an explicit deployment access policy and proxy; this
change does not create a public metrics service.

The three independent-cleanup acceptance tests now force one transient provider
error, then hold that resource's next cleanup action. The other resource must
complete. While the first action is held, a real HTTP read must report an active
queue, active work, a failed action, a scheduled retry, and a successful action.
The response must exclude both resource IDs, the owner token, and the test's
private provider error. Releasing the held action must complete cleanup. The
same tests retain REST deletion checks, API restart, privileged TLS gRPC reads,
conditional cleanup writes, event delivery, and the one-action-per-key check.

This is the same common runtime for database, workload, identity, and count
controllers. Hypershell adds no metric registry, rendering code, or HTTP server.
The [STEGO contract](https://github.com/jsell-rh/stego/blob/b24ac6e7877978988d3df075b4661e6970401c5c/specs/controller-metrics.md)
records collection limits, runtime tests, and measured collector cost. Durable
conditions, cleanup age, persistent retries, distributed ownership, and production
capacity remain open.

The three cleanup workflows and the offline CLI version check passed with race
detection in 19.472 seconds. After stronger checks of the actual HTTP values and
queue reset on shutdown, the three cleanup workflows passed in 18.861 seconds.
These durations include setup. They are not latency or capacity targets.
Controller unit tests, contract tests, and static checks passed. Regeneration
adds two common runtime files and changes the keyed runtime and build record.
The generated, state, and dependency hash set contains 81 files.

This local check uses real PostgreSQL, generated REST and gRPC servers, and a
Kafka protocol fixture. The provider is controlled by the test so that retry and
blocked work are deterministic. It does not establish a full application suite
pass or repeat the real Kubernetes workload gates for this change.

Controller 1.10.0 adds [cleanup summaries](cleanup-summaries.md) through a separate
sampler. Pending counts and the oldest deletion timestamp cover retained work
outside the current queue. Availability and sample-time metrics identify failed
or old reads. These metrics do not replace durable resource conditions.
