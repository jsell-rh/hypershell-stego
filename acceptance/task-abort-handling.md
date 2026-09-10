Hypershell uses STEGO compiler
`f7a630b40d396831bb29ebde874bcf68192e405d` for task abort handling. The generated
supervisor cancels peers and waits for them when a registered callback panics
or exits without return. The final local failure record identifies the task
without the panic value. Hypershell has no custom task recovery code.
The [compiler contract](https://github.com/jsell-rh/stego/blob/f7a630b40d396831bb29ebde874bcf68192e405d/specs/task-abort-handling.md)
defines the behavior and its limits.

`TestGatewayBackgroundTaskAbortAndRestart` builds the generated application with
a temporary Go overlay. The overlay adds a fault task, a cancellation witness,
and cleanup markers to the test binary. It does not edit generated files or add
a production fault switch. A second binary uses the unchanged generated source.

The test runs the following workflow for both panic and `runtime.Goexit`:

1. Create a Gateway through REST and verify its committed owner grant.
2. Receive the Gateway event through the generated runtime and await an empty
   outbox.
3. Trigger the task abort and require process failure.
4. Verify task defers, peer cancellation, and cleanup before caller return.
5. Require one `service.failed` record with the task name in `tasks` and
   `aborted_tasks`. Require the telemetry runtime stop event.
6. Exclude the private panic value, Gateway data, token, and stack text from logs.
7. Restart the normal binary on the same database and verify owner reads through
   REST and gRPC.

The panic case first failed on compiler `8ec724d`: process output contained the
private panic value after Gateway creation and event delivery. With the new
compiler, both cases passed. The task abort test took 6.44 seconds. Eleven
selected application tests passed under race detection with PostgreSQL required
on port 32916, in 71.114 seconds. The other tests cover controller recovery,
request logs, metrics and traces, HTTP diagnostics, startup and database privacy,
local service logs, replica identity, collector failure, and watch-source failure.
Internal and contract race tests and static checks passed. Repeat generation
preserved all 97 output, state, and dependency hashes.

This test covers local failure output. It does not prove OTLP delivery of the
final process failure, forced termination of tasks that ignore cancellation, or
panic handling in additional goroutines and resource constructors or cleanup.
Provider and virtual-machine gates were not repeated locally for this task
supervisor change. Full CI must validate the new revision. The full enterprise
and observability goals remain active.
