# Sandbox count workflow

The count controller runs as `cmd/sandbox-count-controller`. It uses STEGO's
HTTP stream client, Kubernetes list and watch client, gRPC client, transaction,
row lock, storage, and event runtime. Hypershell owns the sandbox classification
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

The private `SetObservedSandboxCount` RPC checks the current Gateway cluster
under the same row lock as the count change. A former cluster cannot change the
count after a Gateway moves. Only configured control-plane subjects can call it.
The count and update event commit together. Equal values emit no event. The
public reference Adjust and Set APIs remain available to control-plane callers.

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
The real sandbox test uses a separate Pod read account. It checks zero, creation,
drift repair, controller restart with an existing Pod, Gateway and database
restart, namespace replacement, and sandbox deletion.

A baseline run on `7db4475` failed after real sandbox execution because REST had
no active count. With the controller, the fresh-cluster Gateway workflow passed
in 299.48 seconds. Recovery and workload tests together took 357.304 seconds with
the race detector. The separate count transport test passed in 9.120 seconds.

The full variant race suite passed; its acceptance package took 404.162 seconds.
Regeneration from compiler `9abcea993bfb0eb1c8d3dcd7f38b0ead0a2b32f5` reported no
drift. The count transport test then passed again in 7.992 seconds with that
output. The count unit tests and static checks also passed.
