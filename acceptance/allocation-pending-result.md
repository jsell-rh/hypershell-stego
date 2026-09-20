# Namespace cleanup rechecks

The namespace controller uses the common STEGO result API. An incomplete cleanup
requests another check after one second. It does not report a failed attempt.
STEGO owns the queue, delay, worker release, cancellation, and telemetry.

Hypershell retains the namespace order. It first removes the Gateway namespace,
then the Sandbox namespace. It retains the console and Gateway state namespaces
until workload and SQL cleanup are complete. The allocation owner can report
completion only after all four namespaces are absent.

The controller uses an explicit completion value from each delete operation.
It commits the allocation observation with the resource version read before the
work. Provider errors, state-write errors, and context failures take precedence
over the completion value. A pending result cannot hide a failed commit. A new
observation that finds retained resources clears an earlier completion value.

The focused allocation check requires the provider-error, state-write, and
cancellation tests. The tests retain the existing placement, namespace order,
resource-version, and finalization assertions. Common STEGO tests cover queue
capacity, worker release, event delays, reconnects, and pending telemetry.

This change requires generated code from the signed STEGO compiler
`7a674e6c038b9fcf9735de78e66a30956c6feb0b`. Generated files must come from the
hosted regeneration job. The adapter, full consumer, REST/gRPC, and live browser
checks must pass before main promotion. The compiler pin alone is not a complete
consumer update.

The 30-second cleanup target remains open. A controller test does not prove
cleanup time, production capacity, or live Kata isolation. Use the complete
Gateway workflow to measure the result.
