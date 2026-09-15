The API and console now use STEGO compiler `2097bea` and `kubernetes-service`
1.9.0. Each generated renderer accepts `--scope all`, `--scope cluster`, or
`--scope namespace`. The default remains `all`.

The operator must install cluster resources before a deployment identity starts
namespace resources. Use the same image digest, namespace, file group, worker or
RPC selection, and endpoint arguments for both scopes. An invalid image or a
missing required endpoint fails before either partial manifest is emitted.

The check covered the API, console, provisioner RPC, namespace allocator,
Gateway workload worker, Gateway identity worker, and Sandbox count worker.
For all seven targets, the default and explicit `all` output match the preceding
release byte for byte. The partial manifests preserve each object's content
and order. Their union contains all objects from the full manifest. Invalid
scopes and missing endpoints produce no output.

Repeated generation preserved all 228 output, state, and dependency files.
The bounded local check rendered manifests only. It made no cluster request
and ran no workload, performance test, or stress test. See
[the recorded results](deployment-scopes.json).

The browser workload runner now uses the separate scopes. The operator installs
cluster resources, and the test Pod checks them before it deploys namespace
resources. The complete live workflow passed. See
[the operator installation evidence](operator-cluster-installation.md).
The test identity still needs its namespace and Secret access restricted before
it can serve as the workload CI identity. The separate Gateway API CI identity
retains its existing restrictions.

STEGO owns scope selection and validation. Hypershell supplies its deployment
and namespace allocation declarations. No Hypershell resource name or role was
added to the compiler for this change.
