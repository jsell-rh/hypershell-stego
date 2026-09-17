# Account and journal cleanup costs

Use the `Account and journal cleanup costs` hosted CI workflow. Do not run the
benchmark on the developer workstation. It requires `STEGO_CAPACITY_CI=1` and
`-benchtime=1x`. The workflow runs three independent samples. It uses the same
CPU, memory, process, filesystem, privilege, and time limits as the
[retained-grant check](retained-history-costs.md).

Each sample has 1,000 active account rows and 1,000 additional provider clients
with no account row. All 2,000 clients have encrypted cleanup journals prepared
through the common Keycloak lifecycle. The Gateway deletion request uses the
domain service. Setup is outside the timed region.

The measured work calls `RecoverGatewayCleanup` until completion. This includes
the generated SQL cursor, saved scan checkpoints, protected journal reads,
the common Keycloak client over verified HTTPS, provider deletion confirmation,
account metadata and audit transactions, final inventory, and scope closure.
After the first 100 accounts, the test reconstructs the SQL store and provider
client. The second page must delete the next 100 clients. This is object
reconstruction, not a process or database restart.

After measurement, every client must be absent except an unrelated client.
Each deletion must occur once. All 1,000 accounts must be closed with exactly
1,000 success audits. Every protected journal must load with valid encryption
and contain closure intent. Registration must be sealed. A maximum of 40 scan
cycles permits bounded source transitions while rejecting lost progress.

The Keycloak endpoint is a controlled HTTPS protocol fixture. These results do
not measure the real Keycloak server, provider discovery of previously unknown
clients, REST or gRPC, concurrent load, other Gateway controllers, or production
capacity. The database uses the explicit literal-loopback TLS test exception.
The maximum RSS includes setup and post-measurement checks. Cumulative allocation
bytes measure work during cleanup and can exceed resident memory.

Keep the exact source, compiler, executable hash, raw measurements, terminal
container state, and resource limits with each result. A missing result is not
a pass. The broader enterprise and application goals remain open.
