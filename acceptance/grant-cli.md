The generated CLI can grant and remove Gateway access. Hypershell declares
role and role-binding commands in `internal/cli/grants.go`. The existing STEGO
runtime supplies parsing, validation, private configuration, HTTPS requests,
and output. The first command workflow required no compiler or generated source
change. The later [immutable apply workflow](cli-immutable-apply.md) uses STEGO
CLI 1.4.0 and extends the same acceptance test.

A user can retrieve their application user ID with:

```sh
hsctl get current-user
```

This ID belongs to the API user record. It is not the OIDC subject. The command
uses the authenticated `users/me` endpoint. It does not decode an unsigned token
or expose credentials. It is an additional helper, not an implementation of
the reference `whoami` command and its token display options.

A Gateway owner can discover a role and create a grant:

```sh
hsctl list roles --search "name = 'gateway:viewer'" --size 1
hsctl get role ROLE_ID
hsctl create roleBinding --gateway-id GATEWAY_ID \
  --role-id ROLE_ID --scope gateway --user-id USER_ID
hsctl get roleBinding BINDING_ID
hsctl list roleBindings --search "gateway_id = 'GATEWAY_ID'" --size 20
hsctl delete roleBinding BINDING_ID --yes
```

The reference names `roleBinding` and `roleBindings` are accepted. The aliases
`role-binding` and `role-bindings` are also accepted. Get and list accept the
singular and plural names. Create and delete use a singular name. Role reads
accept `role` and `roles`. Creation also accepts a JSON object through `--body`.
Deletion requires `--yes`. Lists return one bounded API page as JSON. The
reference table output, automatic pagination, and interactive confirmation
remain open. Unsupported flags cause an error.

`TestGeneratedCLIGrantWorkflow` runs the CLI and API as separate processes. It
uses PostgreSQL, signed identities, verified TLS, and the Kafka protocol fixture.
All user and role IDs come from CLI responses. The test checks the following:

- A user without a grant cannot read or list the Gateway.
- An owner can create a viewer grant with the required API response shape.
- The grantee can read the grant. Another user cannot read or list it.
- CLI and gRPC reads observe the same access change.
- A viewer cannot create or delete grants. Duplicate grants fail.
- The last owner cannot be removed. Deletion requires confirmation.
- A failed event write rolls back deletion and retains viewer access.
- A process restart retains the grant and its access rules.
- Successful removal delivers its event and removes access.
- A new grant after deletion has a new ID and restores access.

The focused race test passed in 5.66 seconds. This includes setup and is not a
latency or capacity measurement. Request-field checks compare CLI definitions
with the application request type. The compiler pin remains
`dc2af4e283d0da07a17bdefd2acf0b97a6a2dd2f`. The broader client port and
production acceptance remain open. See the [CLI port status](cli-port.md).

The full local race suite passed with PostgreSQL and Keycloak required. Its
acceptance package took 470.195 seconds. Static checks and pinned regeneration
passed. The compiler and generated source are unchanged for this workflow.
Hosted checks repeat the complete application and workload gates.
