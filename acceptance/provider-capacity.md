# Real provider account cleanup capacity

The production target is 100 Gateways per instance, with 100 service accounts
per Gateway. Cleanup should complete within 30 seconds after HTTP 202. These
numbers are targets. They are not application limits.

The `Real provider account capacity` workflow tests the account part of cleanup.
It uses the pinned Keycloak image in production mode with a separate PostgreSQL
database. The API uses another PostgreSQL database. Provider HTTPS verifies the
test certificate. PostgreSQL plaintext is limited to an explicit test setting
on the literal loopback address.

The fixture creates 100 Gateway records. Four bounded setup workers create 9,900 provider clients,
service users, and Gateway role grants for 99 background Gateways. The fixture
then seeds their account rows. These accounts supply storage and provider load. The test does not qualify their
creation through the application, token policy, or encrypted journal state. The selected Gateway's 100
accounts must pass REST creation, the generated gRPC provisioner, the common
Keycloak client, and encrypted state storage.

After setup, REST DELETE must return 202 and block new accounts. The generated
runtime must finish account cleanup without a test call to its recovery method.
The timer ends only after the test confirms all of these results:

- The provider state scope is sealed.
- All 100 selected clients and their service users return 404.
- All 100 encrypted journal records authenticate and retain closed identities.
- All 100 account rows are deleted and inactive.
- All 100 cleanup success audit records are present.

The test then compares every other provider client and all 9,900 background
account rows with their saved state. The result contains only counts, timing,
and hashes. It does not contain tokens, client credentials, or journal data.

The hosted CI test process and its API and provisioner children share one CPU,
1 GiB of memory, no swap, and a 256-process limit. Keycloak has two CPUs, 2 GiB,
no swap, and 512 processes. PostgreSQL has one CPU, 512 MiB, no swap, and 256
processes. The test checks its Linux control group before it starts. The test
has a 15-minute limit. CI stops the test unit and removes only provider
containers with this run's labels. No privileged container is used.

Compilation and image download occur before measurement. The record includes
the source commit, source archive hash, compiler pin, Go version, binary hashes,
process exit code, test output, and cleanup result. The test has no race
instrumentation. The separate provider discovery gate retains its race and
restart checks.

This first sample does not qualify total Gateway cleanup. It does not run 100
Gateway Pods, remove their databases or identity clients, measure concurrent
Gateway deletion, or prove a larger installation. A failed timing result must
remain visible. The two-minute cleanup observation limit permits a slow run to
save its completion result; it does not replace the 30-second target.

The first run, `35459418645`, failed during setup. The single realm import did
not finish within eight minutes. No REST account was created, and no cleanup
time was measured. Source and binary hashes matched, and CI confirmed cleanup.
The fixture now uses separate provider API transactions with four workers and an
eight-minute setup limit. This change does not raise the resource limits or
reduce the account count. See [the result record](provider-capacity-evidence.json).
