# Gateway allocation cleanup

Gateway deletion must wait for the namespace allocator. Workload and SQL
cleanup finish before the allocator removes retained state namespaces. Their
completion records do not prove that those namespaces are absent.

The application declares the targeted `allocation` cleanup owner. The allocator
uses the existing STEGO observation runtime and generated cleanup storage. It
checks the Gateway, Sandbox, console state, and Gateway state allocations before
it records completion. The commit uses the resource version read before removal.
A pending result or a failed commit does not indicate completion.

The allocator requires an exact `Gateway` / `cleanup.allocation` grant for its
ManagedCluster. Workload and identity grants do not authorize this observation.
The generated cleanup summary also supplies the allocator's cleanup metrics.

This is a candidate change. Regeneration, focused checks, transport tests,
restart tests, and the complete cluster workflow must pass before promotion.
The new declaration changes the schema generation. Existing installations must
remain rejected until an explicit schema transition is registered and tested.
Do not change generation records by hand to bypass that check.

The live regression uses the existing SQL permission fault. It stops every
allocator Pod before it restores SQL access. Other cleanup owners can then
finish while the state namespaces remain. REST and gRPC must both return the
Gateway in the deleting state. A new allocator process must finish removal and
finalization. The collector requires `allocation-finalization.json`. This new
check is prepared; no live pass is claimed.
