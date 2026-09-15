# Gateway controller write permissions

The API uses STEGO's generated exact grant policy for conditional Gateway
patches and sandbox count calls. Hypershell maps its fields to these operations:

| Operation | Fields | Target |
| --- | --- | --- |
| `observe.workload` | Both `phase` and `status` | Stored ManagedCluster ID |
| `observe.endpoint` | `route_address` | Stored ManagedCluster ID |
| `configure.identity` | `oidc` | Empty string |
| `configure.console` | `console_address` | Stored ManagedCluster ID |
| `observe.sandbox-count` | `active_sandbox_count`, through count RPCs | Stored ManagedCluster ID |

Set `HYPERSHELL_CONTROLLER_WRITE_GRANTS` to a JSON array. For example:

```json
[
  {
    "issuer": "https://issuer.example",
    "subject": "gateway-workload-controller",
    "resource": "Gateway",
    "operation": "observe.workload",
    "target": "the-managed-cluster-id"
  }
]
```

Each conditional patch requires a verified token, a subject in
`HYPERSHELL_CONTROL_PLANE_SUBJECTS`, the current resource revision, and an exact
grant. Missing grants deny writes. Invalid grant configuration stops API setup.
Cleanup grants do not grant these operations. Roles and usernames do not replace
the subject or grant checks. Grant changes require restart of every API instance.
Count calls use the same subject and grant checks inside the Gateway row lock.
They do not accept a caller-supplied revision. An exact count grant does not
permit workload status, identity settings, or console address changes.

Use separate subjects for identity and workload controllers. Give each workload
subject only its required cluster grants. Processes that share a token share all
of that token's grants. A subject can hold several explicit operation grants.

An observation can write `phase`, `status`, and `route_address` together. This
requires both the exact workload and endpoint grants for the stored cluster.
Both groups and one event commit in STEGO's generated serializable transaction.
The first write checks the revision read before external work. The second uses
only that transaction's own revision while it holds the row lock. It does not
read a new external revision or replay stale work. A failure rolls back both
groups and the event. Standalone observation groups remain supported.

Configuration fields cannot be mixed with observations. Other controller patch
fields and empty controller patches are denied. Workload and
console checks use the current stored cluster in the mutation transaction.
The request cannot change placement to obtain another target's permission.
New moves that separate a Gateway from its database are denied. For retained
data from an old move, the former cluster's grant cannot update the Gateway.

OIDC settings are an input to workload reconciliation. An identity write still
uses the revision check and advances the workload's desired generation. This
change does not assign exclusive ownership of OIDC fields: public owners retain
their existing access to these settings.

`TestGatewayControllerWriteGrantsAcrossPlacementAndRestart` checks separate
subjects, missing grants, cleanup-only grants, admin roles, field groups, cluster
new-move denial, restored old placement, REST bypass attempts, and revocation
after restart with the same token. Old placement is restored only with the API
stopped; current database constraints are restored before startup.
It checks unchanged Gateway state on denial. A test database trigger records
committed Gateway events independently of outbox delivery. Successful writes
must commit one Gateway event and deliver it through the generated runtime.
Existing revision tests check stale observations and mixed-field rejection.

The new permission test failed against the previous handler because an identity
subject could publish workload status. It passed with the grant check in 5.20
seconds during the final full run. The required PostgreSQL and Keycloak race
suite passed; its acceptance package took 614.098 seconds. The real Gateway
Kubernetes workflow passed in 229.855 seconds, including deletion before startup
and the live database/identity workflow. Module verification, `go vet`, and
generated-file checks passed.

The generated policy remains in STEGO. This application contains only the field
mapping, environment wiring, and transaction call. No new scheduler or policy
engine is introduced here.

These checks cover conditional Gateway patches and sandbox counts. Other
controller paths, including creation, deletion, grants, catalog writes, and private reads,
still need a complete permission model. Conditional database patches now have
their own [provider grant contract](database-write-permissions.md). Provider
credentials and cross-process fencing remain separate open work.

The public REST patch, generated REST SDK inputs, and CLI apply fields exclude
`route_address`. The gRPC update field remains for assigned controllers. Its
write requires the same revision check as other observations. The generated
`endpoint` observation group hides the old address after a desired generation
change. Publishing or clearing an address does not change the desired generation.
The controller must verify the route before publication; that live route workflow
remains an open acceptance requirement. The combined grant check failed before
this change and now passes. The acceptance test adds a forced failure on the
second SQL write, stale combined writes, endpoint failure and recovery, event
counts, placement changes, and restart. It compiles; its CI result is required.
