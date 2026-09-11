# Gateway creation defaults

The console sends an empty release ID. Its default cluster option also sends an
empty cluster ID. Set these API environment values to registered catalog IDs:

| Setting | Use |
| --- | --- |
| `HYPERSHELL_DEFAULT_GATEWAY_RELEASE_ID` | Release for an empty creation request ID |
| `HYPERSHELL_DEFAULT_GATEWAY_CLUSTER_ID` | Hub cluster for an empty creation request ID |

Each nonempty setting must be a canonical, nonzero KSUID. Invalid settings
prevent service construction. An explicit request ID takes precedence over a
default. The service does not select the first or newest catalog entry.

The service applies defaults only during creation. It checks both references
in the transaction that commits the Gateway, owner grant, and events. If a
required default is unset or its catalog entry is missing, creation fails.
The transaction leaves no new user, Gateway, grant, or event. Creation still
requires the creator role. Database placement remains server-owned.

`TestGatewayDefaultsRemainExplicitAndAtomic` covers selection, explicit IDs,
missing references, access, and rollback. The rendered browser acceptance uses
the same configuration path through the generated API process.
