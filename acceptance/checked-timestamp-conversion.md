# Checked response timestamps

Hypershell uses STEGO `transport.Timestamp` and `OptionalTimestamp` for protobuf
response times. Gateway, grant, catalog, and cleanup summary responses use the
same checked conversion. An absent optional value remains absent. A present
zero Go time remains present.

ManagedCluster, GatewayRelease, and GatewayNetwork responses now reject stored
times outside the protobuf range. Unary and list handlers return no response
and the fixed private Internal error. A watch stops before it sends an invalid
message. Earlier valid messages in that stream remain delivered. The subscription
is closed on exit.

STEGO owns value conversion. Hypershell retains field selection, reference paths,
observation selection, pagination, access rules, and public error status. This
change does not generate complete response mappings or change write ownership.

Hosted regeneration verified all 429 generated and module files, 420 generated
hashes, and 41 input hashes. Only the new common helper, CLI compiler identity,
and three generation records changed. The other 424 files were unchanged.
See the [generation evidence](checked-timestamp-generation-evidence.json).

The focused adapter checks cover invalid created and updated values, all three
catalog services, rejection of partial lists, watch failure, valid wire and JSON
conversion, optional presence, and identity checks before storage access.
The hosted application suite passed 1,294 core cases. All 100 focused timestamp
cases passed, and all seven images passed independent verification. The complete
live workflow passed all 11 required tests. Review checked REST and gRPC access,
denied requests, event delivery, restart, regeneration, telemetry, four browser
views, and cleanup. See the [application evidence](checked-timestamp-application-evidence.json),
[image evidence](checked-timestamp-image-evidence.json), and
[live evidence](checked-timestamp-browser-evidence.json).

Two original read-only test observers selected a failed preflight record. New
readers checked the same live run. A third reader saved all public image records
before a missing local policy stopped it. Independent review matched those records
to the dispatch policy and CI archive. The earlier failures and all replacement
results remain in the evidence. No application test was repeated.

This accepts checked timestamp conversion. Complete declarative response mapping,
production capacity, live Kata isolation, and the enterprise requirements remain
open.

The largest normal cleanup observation with 100 live accounts was 30.84
seconds. The 30-second target remains open.
