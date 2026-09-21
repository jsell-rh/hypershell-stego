The gRPC grant adapter uses STEGO's declared RoleBinding mapper. Hypershell
supplies the role name and username after its domain query. Grant selection,
access rules, list limits, event selection, and watch replay stay in Hypershell.
The REST contract is unchanged.

Compiler `3666fed3` is an immutable, verified release. Generation run
`35576159937` used source `80dd23c`. Independent review checked all 434 generated
and module files, 425 recorded output hashes, and 41 input hashes. Only the new
gRPC mapper, compiler identity, and three state files changed. All existing
mapper bytes and module files remained unchanged.

New adapter checks cover every public field, absent and empty optional values,
wire encoding, output ownership, invalid stored strings and timestamps, and
invalid application-supplied names. The database test creates two Gateways,
corrupts one owner's grant timestamp in its private database, and checks gRPC
list filtering, private errors, and watch termination across API restart. Repair
must restore the complete list. The bounded API gate requires this test.

These tests are required but have not yet passed for this application source.
Regeneration, complete application tests, the live API gate, and the full browser
workflow remain required. This change does not establish production capacity or
complete the enterprise goal.
