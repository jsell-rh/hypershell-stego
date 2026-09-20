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


## Verified workflow

Source `7bc21b7258bf88da5734e2c55c35e594dcbb0903` passed the
[full consumer check](https://github.com/jsell-rh/hypershell-stego/actions/runs/35489043382):
982 core cases across 341 top-level tests, with all 966 prior cases retained.
The rendered browser, web console, and service image jobs passed. The focused
allocation and adapter checks passed at the same source. Kata remains deferred.

The [live Gateway workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35490262586)
passed all 11 required tests with the signed compiler above. Independent review
matched 1,574 source files, 421 generated hashes, and the compiler bytes in the
actual test Pod. Four fresh browser images passed visual review. The workflow
proved account and namespace replacement, denied access, restart recovery,
SQL identity retention, and deletion visibility through REST and gRPC while the
allocator was stopped. Both retained state namespaces remained until recovery.

One Gateway had 100 accounts created through REST with verified token issuance.
Complete cleanup took at most 40.7967 seconds after the accepted deletion response.
All 100 accounts and journals closed; all selected provider clients and users
were absent; all 100 cleanup success audits were present. The previous rescan
workflow observed 57.0464 seconds. Both runs exceeded the 30-second target.
No cleanup stage was observed to return to pending in this run.

The 15 stage observations show account cleanup recorded by 5.4424 seconds and
allocation cleanup recorded by 40.2473 seconds. They are sequential read-return
observations, not exact provider transition times. Bounded allocator logs in the
cleanup window contained 36 pending and 14 successful work completions. No failed
work completion was retained in that window. Sampling and clocks limit these
observations; they do not establish a cause or production capacity.

Independent cluster reads after this workflow found both test fixtures absent,
the shared lease free, and all 32 standing installation resources unchanged.
That read occurred before the separate API gate. This browser record does not
qualify that gate or claim current cleanup after a later run. See the
[complete browser evidence and limits](allocation-pending-live-evidence.json).
The newer controller trace compiler requires its own complete application proof.


## Separate API check

The [API and SQL gate](https://github.com/jsell-rh/hypershell-stego/actions/runs/35491294055)
passed all 52 required tests at source `7bc21b7` with the same signed compiler.
The tests cover atomic Gateway, owner, and event writes; filtered access;
REST and gRPC; event delivery; restart recovery; and durable deletion.
Independent review matched all 1,574 source files and 421 generated hashes.
The committed output matched both generation runs and the final output.
The compiler bytes and public records matched the signed release in the actual
bounded test Pod. The Job had a 1,200-second deadline and no retry.

A separate cluster audit after the API run found both test fixtures absent,
no allocated namespaces or grants, and a free shared lease. All 32 standing
installation resources remained unchanged. The API fixture used PostgreSQL in
a restricted Pod; it does not prove RDS failover. The newer trace compiler,
production capacity, the 30-second cleanup target, and live Kata isolation
remain separate requirements. See the [API evidence](allocation-pending-api-evidence.json).
