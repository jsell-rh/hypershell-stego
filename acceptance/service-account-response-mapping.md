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

These application tests have not yet passed for this source. Regeneration,
complete application tests, the live API gate, and the complete browser
workflow remain required. This change does not prove production capacity or
complete the enterprise goal.
