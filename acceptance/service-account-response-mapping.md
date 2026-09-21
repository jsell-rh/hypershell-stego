The service-account adapter uses STEGO's declared HTTP response mapper.
Hypershell keeps access rules, role selection, connection details, one-time
credentials, and response status decisions. The generated mapper owns scalar
conversion, nullable values, enum checks, timestamp checks, and output storage.

The declaration uses `on_absent: emit_null` for description, revocation time,
and the last error. A missing source value produces JSON null. A present empty
string or zero time remains present. This preserves the existing response
contract. Create, get, list, revoke, and pending delete responses use the same
mapper. A conversion failure returns a private error before response data is
sent. A list with an invalid selected row returns no partial items.

Compiler `9788915c` is an immutable, verified release. Generation run
`35582676497` used preparation source `7b7eb3a`. Review checked all 434 generated
and module files, 425 output hashes, and 41 input hashes. Only the HTTP mapper,
CLI compiler identity, and three generation state files changed. The other
429 files, all existing mapper functions, and all module files are unchanged.
No compiler or Go test ran on the developer workstation.

The new adapter tests cover all public fields, explicit null, present empty
values, public enums, output ownership, invalid strings and timestamps, private
errors, and embedded create/get/list response fields. The database test creates
accounts for an owner and a viewer. It damages one owner's nullable timestamp
and credential type, then checks filtering, denied access, private errors, and
recovery across API restart. Repair must restore the complete list. Revoke and
delete must retain their response contracts. The bounded API gate requires
this test.

Source `64e13ce` passed all 14 hosted groups. The full suite passed 1,469 cases
in 402 roots and retained all 1,422 prior cases. The focused suite passed 256
cases in 31 roots. API run `35585643288` passed all 57 required roots, including
the stored service-account faults before and after restart. Repeated generation
matched all 435 hashes in the API fixture.

Browser run `35586560292` passed all 11 required roots. Its complete scenario
took 538.32 seconds. Review checked the exact source, compiler, seven signed
images, repeated generation, access rules, REST and gRPC, event delivery,
restart, browser sessions, provider and SQL recovery, and durable deletion.
All four captured views were inspected; no layout defect was seen. Logs,
metrics, and traces passed the checks for the expected workers and backends.

Both test fixtures and allocations were absent after cleanup. The shared
lease was free, and all 32 standing resources were unchanged. The measured
Gateway had 100 accounts with verified token issuance. The observed cleanup
upper bound was 34.10 seconds. The 30-second target and 100-Gateway capacity
remain unproved. Live Kata, upstream Sandbox execution, native packet probes,
and RDS failover remain outside this result.

A hosted result reader initially lacked its event policy file. New readers
accepted the same retained result; no test was repeated. The original failure
record remains. See the [workflow evidence](service-account-workflow-evidence.json).
This change does not complete the enterprise goal.
