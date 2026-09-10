# Database watch failures through the generated client

The Gateway deletion-before-startup test found a client defect during the
identity-query change. The database controller stopped with a missing-capability
error, and the database did not become ready. The complete Gateway test passed,
but the gate as a whole failed in 339.638 seconds. The log did not retain the
original RPC status.

STEGO's generated gRPC client copied nil headers into an empty map. The database
controller uses nil headers as the signal to receive the RPC's terminal status.
The copy changed that signal. Thus, a transient failure could become a terminal
capability error. The existing controller regression used a raw gRPC client and
did not cover this wrapper.

Compiler `1fe838c21f891ec1ddd09e4df678c00ce126f95a` preserves nil headers and
still copies present headers. The common fix is in STEGO's grpc-application
component, version 1.6.1. Hypershell's capability requirements and retry policy
did not change.

`TestGeneratedClientPreservesDatabaseWatchFailure` connects the generated client
to a TLS gRPC server. The fixture supplies the RPC outcome; the application runs
its normal watch and replay checks. With the old generated client, the test
failed for Aborted, Unavailable, PermissionDenied, Unauthenticated,
InvalidArgument, and Unimplemented. The package failed in 0.083 seconds.
With the fixed client, the full database-controller unit package passed with
race detection in 1.141 seconds. A clean stream without capability still fails.
Unsupported replay remains a terminal contract error; transient statuses retain
their codes.

The compiler regression also compares raw and generated clients against the
generated TLS server. It checks repeated Header calls, terminal statuses, clean
completion, caller changes to returned metadata, and the following response
message. The full compiler race suite and static checks passed. Compiler CI
run 34489053980 passed.

A separate checkout kept the source of the original application test run
unchanged. With the fixed compiler, login, grant changes across REST and gRPC,
restart, and the concurrent Gateway update check passed in 38.892 seconds.
The complete Kubernetes gate passed in 209.690 seconds: deletion before startup
took 41.23 seconds, and the full Gateway workflow took 167.41 seconds. The gate
covered provider persistence, owner and viewer access, denied requests,
service-account access, Pod and database restart, namespace replacement, offline
deletion, and cleanup after placement changes.

The deterministic client regression establishes the error-classification defect.
The first failed gate's log alone cannot identify its underlying server status.
These results do not prove cross-process fencing, durable retry progress, or a
production recovery-time bound.

The main checkout now has the fixed compiler pin and local component version.
All 231 Go source and dependency files match the fixed-compiler test checkout.
Final race tests passed for the database controller (1.139 seconds), identity
controller (1.016 seconds), and contracts (1.445 seconds). The identity-query and
concurrent-update checks passed in 1.986 seconds. Final static checks passed.
The full query-change suite preceded this pin upgrade; the generated-client
regressions and complete Gateway cluster gate tested the fixed client.
