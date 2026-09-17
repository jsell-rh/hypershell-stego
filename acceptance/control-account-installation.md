# Control account installation

Hypershell uses compiler `931f712` and its common STEGO registry. The generated
policy reserves allocator and declared control-worker account names. A separate
trusted installer creates these accounts after the policy is installed. Neither
CI nor an application worker receives the installer capability. Automatic token
mounting is disabled on the allocator and workload accounts.

The operator installed the generated policy on jshell. The reviewed plan
preserves 25 resources and adds the guard, its binding, and an unbound installer
role. Server validation and policy type checks passed. All 28 installed resources
match the record. Three existing account identities are unchanged. CI gained only
read access to the new policy. The immutable installation record was replaced
with UID preconditions. Installation cleanup passed and the lease was released.

The fixture verifier at `fed0042` passed 68 account cases in
[the plan run](https://github.com/jsell-rh/hypershell-stego/actions/runs/35260879782).
It permits the declared fixture account in the reserved names. It rejects other
account, permission, and guard changes. The earlier failed plan is retained in
[the evidence record](control-account-installation-evidence.json).

The [full live workflow](https://github.com/jsell-rh/hypershell-stego/actions/runs/35261639502)
passed all 11 required tests at `fed0042`. The main test took 687.2 seconds.
Independent checks matched 1,450 source files, 416 generated hashes, the published
compiler, and the actual console image. The workflow includes login, owner
grants, REST and gRPC, filtered lists, denied requests, events, account lifecycle,
and durable deletion. Recovery passed after Gateway Pod, PostgreSQL process,
namespace, and provisioner replacement. Complete browser startup telemetry and
SQL signals passed. All three dashboard screenshots were reviewed.

The workflow recorded 24 ready-Pod account observations across two Gateways and
three namespace incarnations. Account names remained stable after namespace
replacement; account and Pod UIDs changed. Worker account writes were denied.
Independent cleanup found no remaining test resources or held lease.

Hosted checks passed at `a7052b4`: 738 core cases, 313 top-level tests, three
generation checks, the rendered browser workflow, seven images, 231 UI tests,
the generated console module, provider recovery, and 28 required journal tests.
The runtime files are unchanged at `fed0042`; that commit changes only the fixture
verifier and its tests. Source `75f76ba` adds evidence only. The separate
[API gate](https://github.com/jsell-rh/hypershell-stego/actions/runs/35263895912)
passed all 52 required tests at that source. Independent checks matched 1,451
source files, 416 generated hashes across four snapshots, and the live compiler
with its published signature records. The completed Job, its Pods, and owned
fixtures are absent; the shared lease is free.

This result does not establish automatic account retirement or control of
arbitrary external grants. Sandbox setup, live Kata isolation, production
capacity, and the broader enterprise requirements remain open. The PostgreSQL
restart uses the same fixture Pod; it does not establish RDS failover behavior.
