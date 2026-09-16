# Durable Gateway deletion

The approved contract is HTTP 202 after a durable request commit. New account
reservations must then fail. The Gateway remains visible as deleting until every
cleanup owner and retained target is complete. Finalization and the final event
share one transaction. Late cleanup must not restore public visibility.

This work is on `codex/gateway-cleanup-20260916`. It is not qualified for the
default branch. STEGO supplies stored finalization, pending-deletion reads,
checkpoints, bounded scans, scheduling, and HTTP response handling. Hypershell
supplies account policy and the Gateway owner set.

Source `711659a` reproduced the former failure with three retained accounts:
three interrupted requests reached only one account. Source `c0ab6e5` passed
`TestGatewayAccountCleanupRecoveryReachesTailAfterRestart` and
`TestGatewayAccountCleanupRecoveryKeepsPageCheckpoint` on jshell. The first test
proved that an account failure does not stop independent cleanup. The second
proved resumption after a stored page boundary without repeating that page.
Generation hashes matched. Job `gateway-api-06e3e8773918` and its private
fixtures were removed. Tests took 1.18 and 0.53 seconds. These are correctness
checks, not capacity measurements.

An earlier recovery attempt stopped before tests because its compiler build
record lacked Git metadata. The corrected build came from a clean clone with
its revision verified. Preserve both attempts. Results and frozen source are in
`/home/jsell/.local/state/stego/runs/gateway-cleanup-20260916`.

Source `31c8646` passed the generated REST and gRPC deletion gate in 53 seconds.
It proved request and final-event rollback, denied access, filtered lists, account
rejection, pending reads, process replacement, permanent finalization, and Kafka
event delivery. Source `8856413` passed that gate again, both recovery checks,
unchanged-observation event checks, and phase-search checks. STEGO now supplies
the declared deleting phase before filtering, ordering, and pagination.

Generation hashes matched in `projected-result`. Job `gateway-api-5c335707fdf0`,
Pods, and private fixtures are absent. The wrapper failed during Lease release,
after it collected successful tests and removed resources. A separate operator
check confirmed absence and released the same Lease. Preserve the wrapper failure.

The transport gate uses a test account provider and explicit observations for
identity, workload, and SQL. It does not qualify real providers. Source `7836bdc`
removes the old synchronous cleanup path and the unused gRPC provider connection.
Its cancellation, restart, concurrent-account, and late-provider checks passed
in Job `gateway-api-d78167024077`. All five selected tests passed, both generation
passes matched, and the Job, Pods, fixtures, and Lease holder were removed.
The result is in `retired-result`.

The console candidate from `5af38b8` passed 166 tests in CI run `35094901951`.
Its source, compiler, source archive, and asset hashes matched before adoption in
`9e6474d`. It retains pending Gateways, polls their state, and disables incompatible
actions. The earlier candidate failed one old confirmation-text assertion; retain
that failure. The rendered browser check remains required.

Source `6d85104` includes the CLI empty-202 contract and updated deletion tests.
CI run `35095289920` checks core acceptance, the rendered browser, the console,
and the service image. The first CNPG attempt, run `35095613420` at `d7ff741`, failed its SQL cleanup
precheck before the full browser workflow. A cleanup report for a live Gateway
returned gRPC `Internal` instead of `Aborted`. Source `ab0b3ef` returns the storage
state-conflict error. The failed run removed application resources, CNPG runtime,
volumes, and private fixtures. The fixed installation remains. Evidence is in
`cnpg-first-result`. Corrected CNPG run `35096666440` is active, and full CI run
`35096455130` is queued. The earlier run `35095289920` passed its rendered browser,
console, and image jobs; its core suite has not finished.

The full core suite, CNPG workflow, and external PostgreSQL cluster workflow must
pass before this branch can replace the default branch. Kata remains deferred
by the user. Large provider inventories remain open: the inventory call still
uses a bounded full scan. This application retains its fresh-schema gate; no
in-place application upgrade is claimed.

The first full core run, `35095289920`, finished with nine failed tests. Three
failed on the live-resource cleanup status fixed in `ab0b3ef`. Five still expected
a partially cleaned Gateway to be hidden. Source `faccbf5` requires HTTP 200 and
phase `Deleting` in those cases and retains the provider and permission checks.
The namespace-count test found a real defect: pending Gateways in the public
list kept their Pod watches. Source `62ed4b6` checks private stored deletion state
before watch assignment. Its focused race test passed in 5.034 seconds. It also
proves that display text alone cannot stop a live watch. The generated worker
workflow remains a required cluster test. The acceptance package compiled.
The superseded core run `35096455130` was canceled; it is not passing evidence.

CNPG run `35096666440` passed at `ab0b3ef`, with compiler `e01e624`. All prechecks
passed. The complete Kubernetes browser workflow passed in 481.23 seconds. It
proved real login, Gateway creation, access grants, REST and gRPC, events,
controller and API restart, CNPG Pod replacement, encrypted state, SQL isolation,
denied cleanup, recovery, and final Gateway cleanup. The supplied PostgreSQL
server and installation data remained. Correlated worker logs, metrics, and
traces were checked. The run removed its application resources, CNPG runtime,
volumes, and private fixtures, then released its Lease. A separate operator
check confirmed absence. Evidence is in `cnpg-corrected-result`.

This CNPG result precedes the count-watch fix. Full CI `35098200160` and the
external-PostgreSQL browser run `35098646742` use source `371c230`, which includes
that fix. Their results are still pending. The newer source still needs CNPG
qualification before the default branch changes.

The external-PostgreSQL run `35098646742` passed at `371c230`. The complete
Kubernetes browser workflow took 492.27 seconds. Generated hashes matched before
and after testing. It proved supplied-server retention, separate Gateway SQL
resources, denied cleanup, restart, recovery, final cleanup, access rules,
credential use, and worker telemetry. The Job, private fixtures, namespace
allocations, and Lease holder are absent. Evidence is in `external-result`.

The final CNPG run `35100459235` uses `bceea63`, which has the same application
code as `371c230`. It started after external-workflow cleanup was verified.
Full core run `35098200160` remains active. Both results are required before the
Hypershell default branch changes.
