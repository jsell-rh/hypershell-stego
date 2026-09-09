# Gateway controller write permissions

The API uses STEGO's generated exact grant policy for conditional Gateway
patches. Hypershell maps its fields to these operations:

| Operation | Fields | Target |
| --- | --- | --- |
| `observe.workload` | Both `phase` and `status` | Stored ManagedCluster ID |
| `configure.identity` | `oidc` | Empty string |
| `configure.console` | `console_address` | Stored ManagedCluster ID |

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

Each request requires a verified token, a subject in
`HYPERSHELL_CONTROL_PLANE_SUBJECTS`, the current resource revision, and an exact
grant. Missing grants deny writes. Invalid grant configuration stops API setup.
Cleanup grants do not grant these operations. Roles and usernames do not replace
the subject or grant checks. Grant changes require restart of every API instance.

Use separate subjects for identity and workload controllers. Give each workload
subject only its required cluster grants. Processes that share a token share all
of that token's grants. A subject can hold several explicit operation grants.

Each controller patch writes one field group. Mixed groups are invalid. Other
controller patch fields and empty controller patches are denied. Workload and
console checks use the current stored cluster in the mutation transaction.
The request cannot change placement to obtain another target's permission.
After an owner moves a Gateway, the former cluster's grant cannot update it.

OIDC settings are an input to workload reconciliation. An identity write still
uses the revision check and advances the workload's desired generation. This
change does not assign exclusive ownership of OIDC fields: public owners retain
their existing access to these settings.

`TestGatewayControllerWriteGrantsAcrossPlacementAndRestart` checks separate
subjects, missing grants, cleanup-only grants, admin roles, field groups, cluster
movement, REST bypass attempts, and revocation after restart with the same token.
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

This change covers conditional Gateway patches only. Other controller paths,
including creation, deletion, counts, grants, catalog writes, private reads,
and database observations, still need a complete permission model. Provider
credentials and cross-process fencing remain separate open work.
