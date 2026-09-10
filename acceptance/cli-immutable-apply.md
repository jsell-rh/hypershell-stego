RoleBinding apply uses the common immutable resource support in STEGO CLI 1.4.0.
The compiler pin is `0db92903cad5c9e9e360558eec2e90f8f298c53d`.
Hypershell supplies the kind, API path, create fields, and four identity fields.
It adds no apply request loop or conflict handler.

The RoleBinding API returns an ID and the grant fields. It has no name field
and no PATCH operation. Use this manifest with IDs returned by the API:

```yaml
apiVersion: hypershell/v1
kind: RoleBinding
metadata:
  name: example-viewer
spec:
  gateway_id: GATEWAY_ID
  role_id: ROLE_ID
  user_id: USER_ID
  scope: gateway
```

```sh
hsctl apply -f binding.yaml --dry-run
hsctl apply -f binding.yaml -o json
```

`metadata.name` is an optional display label. The exact Gateway, role, user,
and scope values select the binding. Apply creates a missing binding. It
reports an exact existing binding as `unchanged` and sends no write. An optional
`metadata.id` selects an existing record, whose requested fields must still
match. Apply cannot change a binding's identity. It does not delete or replace
a grant. Use explicit create and delete commands to change access.

Lookup uses a filtered list with at most two items. A returned record must match
every requested field. Ambiguous or contradictory results fail. The API's live
unique index prevents concurrent duplicate grants. After HTTP 409 from creation,
the runtime performs one fresh lookup. An exact match permits an `unchanged`
result. It does not retry a write. A retained deleted binding does not prevent
creation of a new live binding with a new ID.

The API retains its access rules and atomic event writes. A matching readable
binding can produce `unchanged` without write permission. This result confirms
observed state only. It does not grant permission to create or delete bindings.
Grant lists remain filtered, denied requests remain denied, and a batch of
manifests is not a transaction across resources.

The reference is Hypershell commit
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`. Its CLI apply mapping does not establish
a name or PATCH contract for RoleBinding. This port follows the registered API.
The separate Role API has only read routes. Role writes require a new API and
access policy. Kustomize rendering and the remaining CLI output options are
still open; see the [CLI port table](cli-port.md).

The extended `TestGeneratedCLIGrantWorkflow` uses PostgreSQL, verified HTTPS,
generated REST and gRPC servers, and a Kafka protocol fixture. It checks:

- Creation through apply, returned grant shape, and event delivery.
- Repeated apply and explicit ID selection with no write.
- A changed identity rejected before a write.
- Denied grant creation and filtered reads.
- REST and gRPC agreement on granted and removed Gateway access.
- Grant retention and unchanged apply after API restart.
- Event-write rollback, last-owner protection, and grant removal.
- Selection of a new live grant after deletion of the old grant.
- Concurrent apply with one created result, one unchanged result, one live
  binding, and the same returned ID.

The common STEGO TLS fixture forces a creation conflict and verifies the bounded
read after that conflict. Concurrent application requests can also complete
without a conflict when the second lookup observes the first creation.

The six CLI workflows and the offline version check passed with race detection
in 93.258 seconds. PostgreSQL and Keycloak were required. The grant workflow
passed in 6.43 seconds. These durations include setup and are not performance
targets. Unit, contract, and static checks also passed. The compiler's full race
suite passed, followed by its final generated CLI regression fixture.

This change adds one generated runtime file and updates two generated CLI files.
It does not change API, database, or provider implementation. The regeneration
check covers all 79 generated, state, and dependency file hashes. The selected
checks do not establish completion of the full application or workload suite.
