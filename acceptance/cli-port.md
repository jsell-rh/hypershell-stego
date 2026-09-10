This is the current CLI port status. The reference is
`components/cli/cmd/hypershell` in Hypershell commit
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`. The application command factory
and executable acceptance tests establish the delivered scope. A command name
alone does not establish compatibility with all reference options.

| Reference area | Delivered behavior | Work still required |
| --- | --- | --- |
| Gateway | Create, get, list, delete, apply patch, body files, filtered API pages | Connection instructions, reference output options, interactive deletion |
| Service account | Create, get, list, revoke, delete, relative expiry, protected credential output | Remaining output and confirmation options |
| Role binding | Create, get, list, delete, grant access and removal | Reference list output and automatic pagination |
| Role | Get and list, as registered by the reference API | Reference list output; role mutation would require a new API and access policy |
| Managed cluster | Create, get, list, delete, apply patch, placement workflow | Reference output options and interactive confirmation |
| Managed database | Create, get, list, delete, apply patch, placement workflow | Reference output options and interactive confirmation |
| Gateway release | Create, get, list, delete, apply patch, placement workflow | Reference output options and interactive confirmation |
| Gateway network | REST and gRPC CRUD and watch; CLI create, get, list, delete, apply patch | Reference output options and interactive confirmation |
| Login and logout | Browser and device OIDC, private token files, refresh, provider token revocation | Legacy configuration migration and remaining reference options |
| Apply | Five resource kinds; files, directories, stdin; local dry run; exact IDs; partial results and failure status | Role-binding mapping, Kustomize rendering; role writes have no reference API route |
| Config | Login writes private configuration | Reference config commands and pager settings |
| Whoami | API-verified identity, protected raw and decoded token output, token refresh | Reference text output format |
| Version and completion | Not supplied | Build identity and shell completion |
| Output | Bounded JSON and private output files | Tables, columns, headers, pager, reference output flags |

`get current-user` returns the API user ID needed for grants.
[`whoami`](cli-identity.md) reports the verified issuer identity and token expiry.
It uses JSON output and requires explicit protected token-output selection.

The reference CLI contains role create/delete commands and a role apply mapping.
However, its [role route registration](https://github.com/openshift-online/hypershell/blob/14256be29bcfe4fff38bcaf4a41511cb394ea8e1/components/api-server/plugins/roles/plugin.go)
registers only GET routes. Its OpenAPI role contract also contains only reads.
The earlier port table incorrectly treated these command stubs as supported
role mutations. The variant preserves the registered read contract. Custom role
writes and dynamic permission evaluation require a separate application policy;
they must not be inferred from unused command scaffolding.

STEGO supplies common command execution. Hypershell supplies command paths,
fields, and domain rules. Provider and API acceptance tests verify actual
behavior. The [Gateway](generated-cli.md), [service-account](service-account-cli.md),
[OIDC](oidc-cli.md), [grant](grant-cli.md), [catalog](catalog-cli.md), [network](gateway-networks.md), and [apply](cli-apply.md) workflows are executable evidence.
The full CLI port remains incomplete. Unsafe reference behavior, such as
bypassing TLS checks, must not become a default or an accepted insecure path.
