# Cluster budget for the full capacity test

On 2026-09-20, the saved jshell context reported five nodes with a total of
7,500 millicores of allocatable CPU. This includes every node. Scheduling
policy, platform services, and other workloads can reduce the capacity
available to the test.

At source `8a5e38d`, each Gateway server requests 100 millicores and 256 MiB
of memory. One hundred Gateway servers therefore request at least 10,000
millicores. This excludes consoles, database services, control-plane workers,
Sandbox Pods, and platform services. The full target cannot fit the current
CPU requests on this cluster, even before those other needs are included.

The [budget evidence](jshell-capacity-budget-evidence.json) records the
source hash, node capacities, read time, and scope. The original node response
is retained with the operator records. This check starts no load test and
changes no resource request or cluster setting.

The target remains 100 Gateways per instance, 100 service accounts per
Gateway, and complete Gateway cleanup within 30 seconds. These are targets,
not product limits. A full test needs a larger suitable cluster, or a resource
profile supported by separate performance measurements. Reducing requests to
fit this cluster would not by itself prove safe performance.

The current bounded workflow uses two Gateways. Its normal deletion sample
contains 100 accounts on the measured Gateway. It can establish correctness
and that cleanup observation. It does not establish the 100-Gateway target.
