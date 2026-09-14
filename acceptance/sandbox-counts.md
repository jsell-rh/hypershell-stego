# Sandbox count workflow

The count controller runs as `out/deploy/workers/sandbox-count`. It uses STEGO's
HTTP stream client, Kubernetes list and watch client, gRPC client, transaction,
row lock, storage, event runtime, and keyed controller scheduler. Hypershell owns the sandbox classification
and cluster assignment rules. STEGO has no sandbox or Gateway types.

A Pod is active when its phase is Pending or Running and its labels contain
`agents.x-k8s.io/sandbox-name-hash`. The controller maps both the original
Gateway namespace and the separate sandbox namespace to the Gateway count.
Pod UIDs prevent a replacement Pod from being confused with its predecessor.
Duplicate events do not add to the count.

The first list forms a complete baseline before any count writes. Ordinary
watch reconnects retain the last resource version. Expired history invalidates
the cache and causes a new list. There is no periodic Pod list in steady state.
A periodic Gateway catalog read also finds Gateways with no active Pods. The
controller sends absolute values from its cache, so repeated writes are safe.
One writer prevents its own older write from overtaking a newer observation.

All three count RPCs require a configured control-plane subject and an exact
`Gateway` / `observe.sandbox-count` grant for the stored ManagedCluster ID.
Set this grant in `HYPERSHELL_CONTROLLER_WRITE_GRANTS`. A workload, identity,
console, or cleanup grant does not permit count changes. Neither an administrator
role nor a count grant without the subject allowlist is sufficient.

The grant check and count change use the same Gateway row lock. The private
`SetObservedSandboxCount` RPC also checks the caller's cluster ID against the
stored cluster. A former cluster cannot change the count after an old move is
restored. New moves that would separate a Gateway from its database are denied.
The count and update event commit together. Equal values emit no event.
The reference Adjust and Set APIs use the same exact grant check, including
when the requested value equals the stored value.

Use one active count controller per managed cluster. Multiple independent
controllers can overwrite a newer count with an older cache observation. The
count remains advisory. It must not control access, billing, quotas, or deletion.
Loss of Pod watch or API access stops the controller. Other API failures retry.
The deployment must restart a failed process and report repeated failures.

Required settings are `HYPERSHELL_MANAGED_CLUSTER_ID`, `HYPERSHELL_API_GRPC_ADDR`,
`HYPERSHELL_API_CA_FILE`, `HYPERSHELL_API_TOKEN_FILE`, `HYPERSHELL_KUBERNETES_URL`,
`HYPERSHELL_KUBERNETES_CA_FILE`, and `HYPERSHELL_KUBERNETES_TOKEN_FILE`.
Use projected tokens and the separate
[Pod read role](../deploy/sandbox-count-controller-rbac.yaml).
That role currently has cluster-wide Pod access. The shared-cluster work must
limit Pod watches to allocated Gateway and sandbox namespaces. The count API
grant check covers API writes; Kubernetes read isolation remains open.
`HYPERSHELL_SANDBOX_COUNT_RESYNC` defaults to two minutes. Its range is one second
to five minutes. It controls Gateway catalog refresh and cache-based repair.

The cache and pending write queue each hold at most 10,000 entries. The Gateway
catalog scan also has a 10,000-entry limit. Each RPC has a five-second deadline.
A Pod cache or event queue overflow stops the controller. An invalid Pod
observation also stops it. A catalog overflow prevents a complete repair pass.
Operators must resolve the limit or invalid source data before restart.

Unit tests cover active phase transitions, duplicate events, replacement Pods,
empty baselines, serialized writes, retry after failure, count drift, and access
loss. The transport test covers REST and gRPC reads, denied writes, atomic event
failure, generated event delivery, and rejection of writes from a former cluster.
It restores old placement only while the API is stopped. Its fixture restores
the current database constraints before API startup. The separate grant test
checks all three count methods, foreign targets, unrelated controller grants,
unchanged rows and committed events after denial, and revocation after restart.
The real sandbox test uses a separate Pod read account. It checks zero, creation,
drift repair, controller restart with an existing Pod, Gateway and database
restart, namespace replacement, and sandbox deletion.

The following historical results predate exact count grants and local database
placement. They do not prove the current permission changes.

A baseline run on `7db4475` failed after real sandbox execution because REST had
no active count. With the controller, the fresh-cluster Gateway workflow passed
in 299.48 seconds. Recovery and workload tests together took 357.304 seconds with
the race detector. The separate count transport test passed in 9.120 seconds.

The full variant race suite passed; its acceptance package took 404.162 seconds.
Regeneration from compiler `9abcea993bfb0eb1c8d3dcd7f38b0ead0a2b32f5` reported no
drift. The count transport test then passed again in 7.992 seconds with that
output. The count unit tests and static checks also passed.

The controller now uses STEGO's `RunKeyed` scheduler. Hypershell no longer owns a
polling timer, retry map, wakeup channel, or worker lifecycle. It supplies an
observer, a catalog scan, and an action that reads the latest count. The changed
namespace set is drained after each cache update; it does not hold pending work.

The queue combines repeated keys. A key changed during a write receives another
pass. Failed writes use exponential delay from one to 16 seconds. New events do
not bypass that delay, and due retries precede newer keys. Capacity includes
queued, delayed, and active keys. A baseline reset pauses new work. An existing
write can finish, but later writes wait for a complete replacement and read the
new count. This advisory count still requires one active controller per cluster.

The local workload gate passed in 317.874 seconds with the generated keyed
runtime. Gateway recovery before controller startup took 57.69 seconds. The real
sandbox workflow took 259.14 seconds. It proved count repair through REST and
gRPC, count-controller restart, access rules, actual sandbox execution, Gateway
and database restart, namespace replacement, and offline cleanup. The count
race suite, including reset during an active write, passed in 3.137 seconds.
These durations include test setup and are not production capacity results.

Pinned generation from compiler
`50e393410fb9eb77ccfc523155f2b7ccbe60c74d` produced the same runtime and client
bytes used by the workload test. The separate count transport workflow passed
in 7.23 seconds with that pin. Application static checks passed. Compiler CI
passed in run `34390663953`; the new application CI run follows publication.

## Exact count grants and local placement

On 2026-09-14, the bounded jshell Job `stego-placement-5ed89300/check` compared
the previous count handler with the exact-grant check. The previous handler
allowed the second cluster's controller to change the first cluster's count.
The fixed handler passed all eight selected checks under race detection in
41.890 seconds. The exact-grant test took 8.68 seconds; the REST and gRPC count
workflow took 11.25 seconds.

The checks cover Adjust, Set, and observed-count calls; foreign clusters;
unrelated controller grants; administrator roles; unchanged records and
committed events after denial; equal values; grant revocation with the same
token after restart; atomic event failure; and concurrent increments. The
placement checks reject new remote moves and preserve separate coverage for
old moved records. Both controller field grants and retained cleanup passed
with the shared legacy-placement fixture.

Results are in `/tmp/hypershell-count-grants-n8dgwwuu`. All 230 generated and
build-file hashes match both generation passes, the post-test files, and the
checkout. The frozen source contains 823 files. Only this document, the
controller-permission document, and the README changed after the source freeze.
The Job completed, and its namespace and private launch files were removed.

The check uses PostgreSQL, the generated API, and the generated event runtime.
It does not run the Kubernetes Pod watcher. Restricting that watcher's read
permissions to allocated namespaces remains a separate required change.
