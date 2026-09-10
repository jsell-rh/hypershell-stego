The identity controller must preserve provider backoff when its API watch
reconnects. Events and transport recovery must not cause an early provider call.
This uses STEGO's shared keyed runtime. The application has no retry map.

Two regressions first failed with compiler
`5a5ebad88f4057417e60d0b35dceecfc1925e959`:

- `TestIdentityRetryDelaySurvivesWatchReconnect` runs the actual identity
  controller with controlled API clients and a failing provider. After three
  failures, it drops the watch. The next provider call occurred after 1.05
  seconds instead of the required four seconds.
- `TestIdentityRetrySurvivesAPIWatchRestart` uses PostgreSQL, the generated API,
  TLS gRPC, REST, and a controlled failing provider. After four failures, it
  restarts the API on the same gRPC address while the controller remains active.
  REST and gRPC recover, and the stored failure condition stays unchanged.
  The next provider call occurred after 3.04 seconds instead of eight seconds.

Compiler `4ca5a064bdee4131aaffbf198d37feec3f1ecabf` retains the bounded queue
across watch sessions. It preserves queued retry due times. It cancels and joins
old callbacks before reconnecting. Interrupted actions receive the next capped
delay. A new connection still starts discovery, and each action reads current
state. The same change applies to workload, database, and count controllers that
use the generated keyed watch adapter.

Both regressions passed under race detection with the new compiler. The
controller package completed in 8.020 seconds. The actual API test completed in
19.422 seconds with PostgreSQL required. Static checks passed. The compiler's
full race suite also passed with PostgreSQL required. Its tests cover queue
capacity, interrupted work, independent keys, due-time preservation, terminal
errors, and shutdown before reconnect.

This evidence covers API restart with a live controller. It does not establish
retry persistence after controller process restart, distributed exclusion,
provider fencing, or safe deletion-history retirement. Those contracts remain
open. The provider in these two tests has controlled behavior; real Keycloak
and workload tests remain separate acceptance checks.

The full application race suite then passed with PostgreSQL and Keycloak
required. The acceptance package completed in 869.854 seconds. All 350 reported
tests and subtests passed. This includes Gateway REST and gRPC access, generated
events, API restart, real Keycloak grant repair, provider deadlines, retained
cleanup, and CLI workflows. The separate local Kubernetes and VM gates were
not repeated; remote CI runs those gates after the commit.

Regeneration changed only saved compiler state, the CLI compiler build record,
and the two generated keyed-controller files among 90 output, state, and
dependency files. Repeated pinned generation preserved all 90 hashes. No
application scheduler or retry state was added.

A later inventory regression found that failed scans still lost their retry
delay on watch reconnect. With compiler `4ca5a06`, both API discovery and provider
inventory resumed after about 1.05 seconds instead of four seconds. The actual
API restart test resumed inventory after 3.02 seconds instead of eight seconds.

Compiler `3d288fa89fa6c57da95f0407a83b14438c1920ba` and controller version 1.12.4
now retain the scan retry schedule across watch sessions. Interrupted scans
receive the next capped delay. Successful scans permit immediate discovery on
reconnect. Resource actions can continue while a failed scan waits. Hypershell
uses this generated contract without a local scan scheduler.

All internal and contract tests passed under race detection. Static checks
passed. Twelve selected application tests passed with PostgreSQL and Keycloak
required in 229.138 seconds. These cover both action and inventory retries
across API restart, the real Keycloak workflow, backlog progress, provider
deadlines, independent cleanup after restart, and watch expiry and failure.
The inventory restart test preserves the client condition; that condition does
not certify a complete provider inventory. The earlier full 350-test result
above applies to the preceding compiler update. The full suite and separate
Kubernetes and VM gates were not repeated locally for this scan update.

Regeneration again changed only saved compiler state, the CLI compiler build
record, and the two generated keyed-controller files among the 90 tracked
output, state, and dependency files. Repeated pinned generation preserved all
90 hashes. Process-restart retry persistence,
distributed ownership, and provider fencing remain open.
