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

Application source `62adcf0` passed all 14 hosted check groups. The full suite
passed 1,422 cases in 397 root tests and retained all 1,404 prior cases. The
focused transport suite passed 214 cases in 27 roots. API run `35579620505`
passed all 56 required roots, including the stored grant fault across restart.
An earlier API run failed while downloading the OpenShift client, before the
application test started. Its failure and cleanup records are retained.

Browser run `35580560335` passed all 11 required roots. It used the exact source,
compiler, and signed images. The full browser workflow took 561.88 seconds.
Review verified login, Gateway creation, grants, filtered and denied requests,
REST and gRPC, event delivery, restart, session rotation, logout, SQL recovery,
and durable deletion. All 435 generation hashes matched across repeated runs
and after the tests. All four rendered images were viewed. No layout defect
was seen. Logs, metrics, and traces were correlated for the expected workers
and browser backends.

Both test fixtures and their allocations are absent. The shared test lease is
free. All 32 standing cluster resources are unchanged. A trace evidence reader
initially used the previous full CI run path. A separate corrected reader
verified this source and all unchanged trace requirements against the same
collected result. No cluster test was repeated for that reader error.

The measured Gateway had 100 accounts with verified token issuance. Cleanup
and all completion checks took approximately 31.65 seconds in this observation. The
30-second target is not proved. This is not a 100-Gateway capacity test. Live
Kata and upstream Sandbox execution remain deferred. RDS failover and the
remaining enterprise requirements are not proved.

See [the evidence record](grpc-grant-workflow-evidence.json) for exact run IDs
and proof hashes. The nullable service-account mapper is separate work.

Exact main source `c42b2ed` also passed all 13 automatic hosted groups and API
run `35582475435`. Review checked 1,422 core cases in 397 roots, 214 focused
cases in 27 roots, all 56 required API roots, all 434 generated and module
files, and seven signed images. API cleanup removed both test fixtures and
allocations, released the shared lease, and preserved all 32 standing resources.
Only four acceptance documents differ from the accepted browser and event
source `62adcf0`. No separate main browser or event run is claimed. See the
[main evidence](grpc-grant-main-evidence.json). The service-account candidate
and the remaining enterprise requirements are still open.
