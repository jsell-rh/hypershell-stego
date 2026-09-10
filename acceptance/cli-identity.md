# CLI identity reporting

`hsctl whoami` reports the identity accepted by the configured Hypershell API.
The generated command refreshes an OIDC session when required, then calls
`GET /api/hypershell/v1/users/me` with the selected access token. Token-file
sessions use the current contents of the private token file.

Default output is JSON with `username`, `issuer`, `subject`, `expires_at`, and
`api_url`. A nonempty email is included as `email`. It contains no access token, refresh token, or
unselected response fields. The subject identifies the caller at the issuer.
It is distinct from the application's user ID. The expiry belongs to the access
token used for this request; it does not expire the user record.

Token export requires an explicit destination:

```sh
hsctl whoami --show-token --output-file access-token.txt
hsctl whoami --show-token-decoded --output-file claims.json
```

`-t` is an alias for `--show-token`. Use `--output-file -` only to explicitly
select stdout. The two token modes are exclusive. Output files use mode 0600
and cannot replace existing files. A failed API check leaves no exported token
or empty reserved file. Decoded output preserves exact JSON numbers and contains
the claims of the token accepted by the API.

STEGO supplies command parsing, credential refresh, HTTPS calls, response checks,
and output protection. Hypershell supplies the current-user path and projects
issuer, subject, and expiry from the generated verifier's context. The command
does not infer identity from an unverified local JWT. It requires the API to be
reachable, including for token export. Default JSON output, online verification,
and explicit token-output selection differ from the reference CLI.

The current-user extension is now version 1.1.0. It adds the required response
fields `issuer`, `subject`, and `expires_at`; existing user fields and IDs retain
their meaning. Strict clients must use the updated extension schema. The new
identity command rejects an older response that lacks these fields. Requests
still cannot select another user, supply a query, or send a request body.

The Gateway CLI workflow checks default output, protected raw and decoded token
files, existing-file rejection before API contact, forged and expired tokens,
token-file rotation, and identity after API restart. A caller who can display
its own identity still cannot read another user's Gateway. The OIDC workflow
checks browser and device identities and runs `whoami` beside two resource reads
when the shared session needs refresh. It then verifies identity after the new
tokens are saved.

The [STEGO identity contract](https://github.com/jsell-rh/stego/blob/main/specs/cli-identity.md)
records trust assumptions and limits. This change does not provide a user
directory, token introspection for other callers, or immediate revocation of
already issued tokens.

All eight selected workflows passed in 103.272 seconds with PostgreSQL and
Keycloak required. They comprise the six CLI workflows and two current-user
checks. OIDC identity and concurrent refresh passed in 36.83 seconds; the Gateway
CLI and protected token-export workflow passed in 5.73 seconds. CLI, HTTP, and
contract race tests and static checks also passed. These durations include test
setup and do not establish production capacity. The full application and
Kubernetes suites were not repeated locally for this identity change.
