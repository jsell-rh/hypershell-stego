# Common worker connection settings

The Gateway identity worker, Gateway workload worker, and account provisioner
use the existing STEGO `ControlAPI` configuration group. The workload worker
also uses `ClusterWorker`. These groups already serve the namespace allocator
and Sandbox count worker. This change adds no environment names or generated
configuration types.

The entry points load the selected groups before they construct a connection.
Invalid scalar input returns a private configuration error before certificate
or token file access. Provider constructors still check addresses, trust,
credentials, and access. Their cleanup calls remain in place.

Hypershell retains caller grants, instance identity rules, provider state
journals, optional dashboard dependencies, and OpenShell settings. The separate
optional provisioner connection retains its existing enablement rules.

The new checks supply invalid values for each control connection field in all
three entry points. They require the typed field error and check that it does
not expose supplied values. A separate check requires invalid cluster settings
to fail before the workload worker opens its control connection.

This branch requires hosted application checks and complete workflow evidence
before main promotion. No compiler or application binary was run locally.
