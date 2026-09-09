The first generated CLI workflow uses a private token file to create, retrieve,
list, and delete a Gateway. STEGO generates the executable entry point, command
runtime, configuration storage, and HTTPS client. Hypershell declares its public
command names, paths, and request fields in `internal/cli`. The CLI does not
import the reference CLI or rh-trex-ai.

Build it with `go build -mod=readonly -o bin/hsctl ./out/cli/cmd`.
Configure it with:

```sh
bin/hsctl login --url https://api.example.test \
  --token-file /absolute/path/access-token \
  --ca-file /absolute/path/api-ca.pem
```

Omit `--ca-file` to use system CA roots. The token file must have no group or
other permissions. Login stores the file path and reads the current token on
each command. It does not copy the token or verify it with the API at login.
`HYPERSHELL_CONFIG` selects a configuration file. The default is
`hypershell/config.json` below the operating system's user configuration
directory. New directories use mode 0700; the file uses mode 0600. Existing
configuration directories must not permit writes by other users.

Create a Gateway with catalog IDs from the API:

```sh
bin/hsctl create gateway --name example \
  --cluster-id CLUSTER_ID --release-id RELEASE_ID --database-id '' \
  --server-dns-names '["gateway.example.test"]'
bin/hsctl get gateway GATEWAY_ID
bin/hsctl list gateways --size 20 --search "name = 'example'"
bin/hsctl delete gateway GATEWAY_ID --yes
bin/hsctl logout
```

Creation also accepts `--body FILE` with a JSON object. The body file cannot be
combined with field flags. Optional request fields use their reference names
with hyphens. JSON arrays supply string-list values. The create field inventory
is checked against the domain request type. Get also accepts the `gateways`
alias. List supports page, size, search, and `--order-by`. Successful data
responses are JSON. Delete returns no response body. Logout removes
configuration and leaves the externally owned token file in place.

The test starts the generated REST and gRPC API process with PostgreSQL and the
TLS Kafka fixture. A TLS proxy fronts its local HTTP listener. The separate CLI
process loads a token, creates a Gateway, and verifies an owner grant. A gRPC
read checks the same resource and DNS array. The CLI then checks filtered lists,
a rotated token, denied reads, and the absence of another owner's resources.

After identity synchronization, a constraint rejects the Gateway creation
event. The CLI receives HTTP 500, and the database record, Gateway, owner grant,
and queued events roll back. After API restart, the saved configuration and owner grant
still work. Deletion requires explicit confirmation with `--yes`, delivers its
event, and removes the Gateway from both CLI and gRPC reads. Logout preserves
the external token file. The full workflow passed locally in 4.95 seconds with
the race detector. This duration includes test setup and is not a latency or
capacity measurement.

The common client requires HTTPS with verified certificates, rejects redirects,
ignores environment proxy settings, and bounds headers, bodies, and time. CLI
requests have a 15-second deadline. No application retry loop can repeat a
mutation. A failed response can leave an uncertain result; retrieve current
state before retrying a write. Error output omits server response bodies and
credentials. Root CLI tests also cover malformed and duplicate JSON, invalid
Unicode, file permissions, FIFO inputs, token rotation, and invalid commands.

This is a Linux CLI workflow. Browser and device login, refresh tokens, legacy
configuration migration, interactive delete prompts, connection instructions,
other resource commands, and protected credential output remain open. The old
`--token` and `--insecure` flags are not accepted. Input and output bounds are
specified in the pinned STEGO CLI component documentation. The complete client
port and enterprise goal remain active.

The full local variant race suite passed with PostgreSQL and Keycloak required;
its acceptance package took 456.425 seconds. After the route fix and final pin,
the focused CLI workflow and request-field check passed again. Regeneration and
static checks passed. The pinned compiler is
`1810c7c91a5a29c41c7ad25fad97e1a9a4a06795`. The hosted run repeats the full
application, database, Gateway, and sandbox checks against the final commit.
