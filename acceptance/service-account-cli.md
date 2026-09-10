The generated CLI now supports service-account creation, retrieval, filtered
lists, revocation, and deletion. The CLI uses the generated HTTPS client.
The API calls the generated gRPC provisioner, which manages the identity in
Keycloak. Hypershell defines the command names, API paths, and request fields.
STEGO supplies path binding, parsing, transport, and protected output.

After CLI login, run:

```sh
mkdir -m 700 credentials
bin/hsctl create service-account --gateway-id GATEWAY_ID --name automation \
  --role openshell-user --output-file credentials/automation.json
bin/hsctl get service-account ACCOUNT_ID --gateway-id GATEWAY_ID
bin/hsctl list service-accounts --gateway-id GATEWAY_ID \
  --status ready --search automation --sort name --order asc
bin/hsctl revoke service-account ACCOUNT_ID --gateway-id GATEWAY_ID
bin/hsctl delete service-account ACCOUNT_ID --gateway-id GATEWAY_ID --yes
```

The singular `serviceAccount` and plural `serviceAccounts` aliases are accepted.
List also accepts `service-account` and `serviceAccount`.
Create accepts name, description, credential type, role, and absolute expiration
through `--expires-at`. Omitted role and expiration use the API defaults. List
accepts page, size, status, literal search, sort, and order. Delete requires
`--yes`. Revocation stops new token issuance. Existing access tokens can remain
valid until their expiration. Service accounts do not inherit the creator's
OpenShell workspaces. A Gateway administrator must grant workspace membership
separately to an `openshell-user` subject.

Creation requires an explicit output choice. The file must be new, and its
parent directory must exist without write permission for other users. The CLI
reserves a mode-0600 file before the API request. Existing files and symlinks
are rejected. A successful file write produces no stdout output. Explicit
`--output-file -` sends the credential to stdout for callers that need a pipe.
Error responses omit server response bodies. Later account reads never return
the secret. Logout leaves caller-owned credential files in place.

A write or sync error keeps the file for inspection and cannot send the secret
to stdout instead. An empty reservation is removed after an HTTP or response
validation error. A crash can leave an empty or partial file. An uncertain
request can have created the account; inspect account state before a retry.
The API cannot return the same secret a second time. Revoke and replace the
account if its credential cannot be recovered.

`TestGeneratedServiceAccountCLIWorkflow` starts PostgreSQL, real Keycloak,
the generated provisioner, and the generated API. A local TLS proxy fronts
the API listener. A separate CLI process creates
an account and writes its credential to a private file. The test checks the
reference response schema, actual token issuance and roles, filtered access,
no secret in later responses or stored account data, denied privilege elevation,
API and provisioner restart, revocation, deletion, and logout. It also proves
that a missing output choice or existing output file cannot create an account.
The full acceptance job requires this test through `STEGO_REQUIRE_KEYCLOAK=1`.

The request-field test compares CLI metadata with the domain create type.
Generated Record tests in STEGO check output-file failures, path validation,
request-body separation, and explicit stdout output without Hypershell types.

Explicit `--output json` and `-o` flags, reference CLI response notes, interactive
delete prompts, and the remaining resource commands are still open. Browser and
device login and token refresh are covered by the [OIDC workflow](oidc-cli.md).
This workflow does not establish production
capacity or complete the client port. The full enterprise goal remains active.

The full local race suite passed with PostgreSQL and Keycloak required. Its
acceptance package took 410.041 seconds. The final service-account CLI workflow
then passed in 26.60 seconds, including the reference list aliases and explicit
cross-Gateway denial. The request-field tests passed. These durations include
setup and do not measure production capacity. The compiler is pinned to
`f678fb295b221fe158659ff5ab37d59ff5455760`; its hosted checks passed.

The CLI now accepts `--expires-in 30d` or `--expires-in 2h` on both service-account
creation aliases. This flag is exclusive with `--expires-at` and `--body`.
STEGO converts the duration to the existing `expires_at` property. A day means
24 hours. Compound Go durations, such as `1h30m`, are supported; fractional days
and mixed day/hour syntax are rejected. Values must be positive and must fit a
Go duration and RFC 3339 calendar timestamp.

Hypershell declares the flag mapping. The generated command runtime owns parsing,
conflict checks, UTC conversion, and output reservation. The API retains the
one-hour minimum and 365-day maximum lifetime. Network delay and local clock
error can affect requests near a policy boundary; the CLI does not extend the
requested expiry to bypass the server check. Absolute timestamps and omitted
expiry fields retain their previous behavior and defaults.

The service-account workflow now checks both day and hour durations against the
CLI execution interval. Invalid syntax and conflicting flags must make no HTTP
request and leave no output file. Valid durations of 30 minutes and 366 days must
reach the API and receive HTTP 400 without an account or credential file. The
selected expiry survives API and provider restart. Real Keycloak token issuance,
scope checks, denied elevation, revocation, and deletion remain part of the test.
The [generated timestamp contract](https://github.com/jsell-rh/stego/blob/main/specs/cli-relative-time.md)
records the common mechanism and input bounds.

The updated service-account workflow passed in 26.83 seconds with PostgreSQL and
Keycloak required. All six generated CLI workflows passed in 87.367 seconds:
catalog, apply, OIDC login, Gateway, grants, and service accounts. CLI and contract
race tests and `go vet ./...` also passed. These tests cover the changed command
runtime. The full application and Kubernetes suites were not repeated locally
for this CLI change; API and provider implementations are unchanged.
