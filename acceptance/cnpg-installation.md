The CNPG test installation must supply PostgreSQL before any Gateway controller
starts. It must use a dedicated namespace that is not a Gateway allocation.
The API has no database catalog, database ID, or server registration call.
Gateway controllers own logical databases and logins through STEGO's SQL runtime.
They must not create CNPG Cluster, Database, or DatabaseRole resources.

`scripts/cnpg-test-operator.py prepare` now requires an explicit
`--database-namespace stego-cnpg-database-<suffix>`. It no longer accepts a
catalog ID or derives a namespace from one. Its operator watch and webhooks
select that namespace. The operator starts with zero replicas. Its existing
30-minute lifetime Job, resource limits, and ownership journal stay in place.
The caller must create and remove the installation database namespace while
the operator can process finalizers. The helper refuses existing resources,
a repeated plan, or operator removal before the database namespace is absent.

The old `check-cnpg-database.py` runner is removed. It required deleted catalog
tests, registered a database through the retired API, and checked the old
allocator profile. Historical evidence retains its source hashes. Its source
is available at [the last runner revision](https://github.com/jsell-rh/hypershell-stego/blob/07750dc7160f49099ea7c898a622b87737f4c2a4/scripts/check-cnpg-database.py).

The next installation fixture must run the complete Gateway browser workflow
against a real CNPG server. It must keep the API and session database separate,
use verified TLS, give workers a limited SQL account, and preserve installation
data after Gateway deletion. Namespace and Pod selectors must keep the worker's
network access valid when CNPG replaces a database Pod. The workflow must also
verify a real CNPG restart or failover, retained SQL identities and keys, and
complete resource cleanup. Server creation belongs to the test installation.

Four small local tests passed for namespace validation, watch and webhook scope,
resource and time limits, existing-resource refusal, and cleanup ordering.
The helper change does not prove CNPG application behavior. The required CNPG
CI gate remains unsuccessful until the full installation workflow passes.
