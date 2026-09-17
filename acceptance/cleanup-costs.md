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

## Initial production target

The user set these targets on 2026-09-17:

- 100 Gateways per instance
- 100 API service accounts per Gateway, or 10,000 across the instance
- Gateway cleanup within 30 seconds with healthy dependencies

Measure cleanup from acceptance of the durable deletion request to confirmed
cleanup of the Gateway's owned resources, accounts, SQL state, and credentials.
A quick HTTP 202 response does not complete this measurement. Retain resources
that the ownership and retention policy requires, including an external
PostgreSQL server supplied by the operator.

These values are performance targets. They must not become schema bounds,
registration limits, or fixed compiler limits. Larger installations can have
thousands of Gateways when their resources and providers support the load.
Record hardware, provider versions, active load, deletion concurrency, and
latency samples with each capacity result. Check degraded dependencies and
larger scales separately. Run bounded capacity tests only in CI or the cluster.
The protocol-fixture baseline below does not prove this production target.

## Verified baseline

[Run 35203107357](https://github.com/jsell-rh/hypershell-stego/actions/runs/35203107357)
passed at `aade64b9132e7b7d136871cf87f3a23918177409`, with compiler
`00573709fb15a2a54de4242aa8fdbabee325179a` and Go 1.26.8 on Linux amd64.
All three required samples passed. Each sample completed 1,000 account closures,
2,000 provider deletions, 1,000 success audits, and 2,000 protected journal
checks in 30 scan cycles. The unrelated client remained intact. The second
page reached the next 100 accounts after store and client reconstruction.

| Sample | Timed cleanup | Cumulative allocated bytes | Process maximum RSS |
| --- | --- | --- | --- |
| 1 | 4.4133 s | 232,868,936 | 50,339,840 bytes |
| 2 | 4.3863 s | 232,845,976 | 51,200,000 bytes |
| 3 | 4.0940 s | 232,845,816 | 51,240,960 bytes |

Median cleanup time was 4.3863 seconds. Maximum process RSS was about 48.87 MiB.
The hosted runner reported an AMD EPYC 9V74 CPU. The result applies to this
bounded, low-latency HTTPS fixture. It does not establish a production target.

The shared container-limit verifier also passed its
[grant baseline repeat](https://github.com/jsell-rh/hypershell-stego/actions/runs/35203107150)
at the same source. All six samples passed. Median full-scan times were
0.1107 seconds for 10,001 references and 1.0702 seconds for 100,001 references.
That runner reported an Intel Xeon Platinum 8370C CPU. Do not treat timings from
different hosted runners as a controlled hardware comparison.

Independent checks matched both source archive hashes, compiler pins,
toolchain versions, executable hashes, raw results, terminal container states,
and hard resource limits. Both jobs used identical executable bytes. Neither
downloaded executable was run locally. CI verified removal of the test containers;
there was no independent operator inspection of the hosted runners after cleanup.

| Record | SHA-256 |
| --- | --- |
| Source archive | `b8f75f31613b384f7f7c34e5be80f3e1e12fd2bf6ad2f6a24eb2d6c8497e75b1` |
| Shared test executable | `30cb021ac7a40ffab7813309aa6c3de6a1fdcd1132638bc2277c245a281ad12c` |
| Cleanup measurements | `6420df08e1a659f93148803528805c3f8e401527c43c66dc9636859a24e5913d` |
| Grant measurements | `95f378752db8e20f5805c8e37a987ab15f4e5b6fbf24c8fe8354459b1ee7da30` |

This establishes the first measured account and protected-journal cleanup path.
Real Keycloak server capacity, previously unknown provider clients, concurrency,
complete process recovery, other Gateway controllers, and production SLOs remain
open. No generated runtime change was needed for this fixture.
