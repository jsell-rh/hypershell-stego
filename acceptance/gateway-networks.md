Gateway networks now have REST create, get, patch, delete, and list operations.
The generated gRPC service supplies the same operations and a watch stream.
The generated CLI can create, get, list, and delete network records. Hypershell
supplies the fields, API mapping, and access policy. STEGO supplies storage,
transactions, transport execution, event delivery, and command execution.
No compiler change was needed.

The reference is Hypershell commit
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`. Its network controller records events
but does not create tunnels or change the data plane. This port has the same
limit. The name, topology, tunnel mode, hub Gateway ID, and status are stored
metadata. A hub ID does not grant access to that Gateway. It is not a foreign
key, and it does not prevent Gateway deletion. Network deletion does not delete
a Gateway. A future tunnel workflow needs explicit rules for Gateway membership,
access, and controller ownership before it can interpret these fields.

The current shared-record policy is the same on REST and gRPC:

| Caller | Read, list, watch | Create, patch, delete |
| --- | --- | --- |
| Platform administrator | Yes | Yes |
| Configured controller subject | Yes | Yes |
| Gateway creator | Yes | No |
| Gateway owner or viewer without either global role | No | No |
| Other authenticated caller | No | No |
| Caller without verified authentication | No | No |

This policy follows the existing placement catalog policy. It is a deliberate
change from the reference fallback rules. The reference lets creators change
all networks; its gRPC fallback also permits broader access by Gateway owners
and viewers. The port uses one domain check for both transports. A design
question about shared records versus network owner grants remains open. The
current implementation uses shared platform records until that choice changes.
The role API reports the corresponding network permissions.

Creation assigns a KSUID. Responses retain the reference kind, href, timestamps,
field names, and protobuf field numbers. The generated descriptor also preserves
the removed fleet field numbers and names. Text fields have explicit bounds.
Invalid Unicode, NUL, duplicate JSON keys, unknown fields, and client IDs are
rejected. Patch preserves fields that are absent or null, as in the reference.
An explicit empty optional string remains present. Lists use bounded pages and
stable ID ordering. REST supports the generated search and order rules.

Each successful mutation stores its event in the same transaction. A failed
event insert rolls back creation, patch, or deletion. Watch events use committed
state and validate access. Delete events use retained database rows. The watch
is a live stream. Clients must connect and then list current state after a
restart. Durable event delivery resumes from the generated queue after restart.

CLI examples:

```sh
hsctl create gatewayNetwork --name regional --topology hub-spoke \
  --tunnel-mode wireguard --hub-gateway-id GATEWAY_ID --status planned
hsctl get gateway-network NETWORK_ID
hsctl list gatewayNetworks --size 20 --order-by 'name asc'
hsctl delete gatewayNetwork NETWORK_ID --yes
```

Creation also accepts `--body FILE`. The aliases `gatewayNetwork` and
`gateway-network` are accepted. Get and list also accept plural names. The CLI
returns JSON. Reference tables, automatic pagination, and interactive prompts
remain open. CLI patch still needs the missing `apply` command.
See the [CLI port table](cli-port.md).

Stop the API before an upgrade. Apply earlier migrations, then apply
`migrations/000008_gateway_networks.sql`. It creates the network table and index
and updates role permission metadata. It has lock and statement timeouts. It
preserves role IDs and does not change current metadata on repeat. The API does
not run schema migrations during startup. No reference database was changed.

`TestGatewayNetworkWorkflowThroughGeneratedRuntime` uses PostgreSQL, signed
identities, a Kafka protocol fixture with mutual TLS, and separate generated
API and CLI processes. It checks REST and gRPC CRUD, response shapes, CLI use,
filtered lists, access denials, Gateway ownership limits, validation, null and
empty values, watch events, rollback for every mutation, offline event delivery,
restart, deletion, and an upgrade from the previous schema. The upgrade test
also checks the database name constraint and role metadata. The separate
descriptor test checks every protobuf message and service in the source file.

The focused race workflow passed in 5.61 seconds after the final seed change. CLI field checks and the
reference descriptor check passed. This duration includes setup. It is not a
capacity measurement. The complete application suite and hosted workload gates
remain separate checks. Production broker behavior and network tunnel execution
are not established by this workflow.

The full local race suite found one stale role-discovery expectation. It did
not include network permissions. The expectation and the repeatable role seed
now include those permissions. The affected role, migration, and network checks
passed after the change. The complete suite runs again in CI. The first full
local run is not recorded as a pass.
