The generated CLI supports browser and device login with a public OIDC client.
STEGO supplies discovery, PKCE, the local callback, signed ID-token checks,
private session storage, refresh, and token revocation. Hypershell supplies its
client ID and domain command definitions. There is no rh-trex-ai dependency.

Build with `go build -mod=readonly -o bin/hsctl ./out/cli/cmd`. For browser login:

```sh
bin/hsctl login --url https://api.example.test \
  --issuer-url https://identity.example.test/realms/hypershell
```

The default client ID is `hypershell-cli`. Use `--client-id ID` to select another
public client. Register the loopback callback path `/callback` and permit a
random port on `127.0.0.1`. In the tested Keycloak version, the registration is
`http://127.0.0.1/callback`. Enable authorization code flow and require S256
PKCE. The CLI requests only the `openid` scope. Configure the API audience and
roles in the provider. API tokens and ID tokens serve different purposes.

Use `--no-browser` for device login and enable device authorization on the
client. Open the printed verification URL in a browser and enter the user code.
The CLI does not receive the password. Use `--ca-file FILE` for an explicit API
CA and `--issuer-ca-file FILE` for an explicit provider CA. Omit either flag to
use system roots for that service. TLS certificate checks cannot be disabled.

The configuration stores tokens in a file with mode 0600. New directories use
mode 0700. `HYPERSHELL_CONFIG` selects the file. Near token expiry, a command
refreshes and saves the session before it sends an API request. A process lock
protects rotation when commands run at the same time. An uncertain refresh result leaves a pending record and requires a new login.
If replacement succeeds but its directory sync fails, a later command can
find the complete new session. Neither path reuses the old refresh token.

Run `bin/hsctl logout` to attempt provider token revocation and remove local
configuration. A provider failure still removes local credentials and returns
an error. This operation does not clear browser cookies or log out other
applications. The private lock file remains in place.

`TestGeneratedCLIOIDCWorkflow` uses a real TLS Keycloak provider, PostgreSQL,
the generated API, and a separate generated CLI process. Its test browser only
submits provider forms. The CLI owns the callback and token exchange. The test
creates a Gateway after browser login, restarts the API, waits for token expiry,
and retrieves the Gateway from three concurrent CLI processes. It checks that
rotation is saved. Device login as another user must produce an empty list and
a denied Gateway read. Logout must remove local credentials and prevent reuse
of the provider refresh token.

The existing [Gateway CLI workflow](generated-cli.md) covers atomic Gateway,
owner-grant, and event writes, REST and gRPC reads, denied access, event delivery,
rollback, and restart. Both workflows run in the full acceptance suite. The
suite permits 12 minutes because the earlier hosted run took 571 seconds
before this workflow was added. Individual network deadlines are unchanged.
Regeneration uses the committed compiler pin and checks generated state.

This is a Linux CLI. Legacy configuration migration, interactive confirmation,
remaining reference options and commands, other clients, and production
acceptance remain open. A passing workflow does not prove all deployment
conditions or capacity limits.

The focused workflow passed locally in 38.21 seconds with the race detector.
It used Keycloak 26.7.3 from the pinned image and compiler
`dc2af4e283d0da07a17bdefd2acf0b97a6a2dd2f`. This duration includes setup and
is not a latency or capacity measurement. The test selects its existing
`hypershell` public client with `--client-id`; the application default remains
`hypershell-cli`. The test browser requests HTML and retains provider cookies.
The application neither submits login forms nor receives the user's password.

The complete local race suite passed with PostgreSQL and Keycloak required.
Its acceptance package took 441.456 seconds. Static checks passed, and the
full dependency scan found no known vulnerabilities. Pinned regeneration
reported no drift before the run. Hosted checks repeat the application,
database, Gateway, and sandbox workflows after the commit.
