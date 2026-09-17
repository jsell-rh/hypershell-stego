# Common database credential preparation

Gateway and console setup use STEGO's `PrepareDatabaseCredentials` operation.
The common component checks the server identity, stored SQL ownership record,
and SQL names under one resource lock before it returns candidate credentials.
Hypershell selects the resource key and secret fields. STEGO's existing secret
state operation retains the selected candidate before database creation.

The SQL workflow also requires a deleted Gateway's retained SQL record to reject
new application credentials. Existing checks cover separate logical databases,
limited logins, cross-database denial, stored keys, restart, and cleanup.

This branch selects compiler and common registry
`b8fdfd6946a740430a2a4f41cf9aaae03a6d6793`, including `postgres-client` 1.5.0.
The common compiler, SQL lifecycle, provider, storage, and example checks passed
in run `35217276981`. The signed main package from run `35218936816` was checked
against the exact source and two independent builds. Its immutable release and
common installer were also checked. The compiler SHA-256 is
`2913c048a2ddd64ca55af9ad4af485167ea6c07718fa55c577b475cb55608160`.

CI regeneration passed in run `35219768157`. Independent checks matched all
415 archived files, the exact source, the installed release record, and all
three drift checks. The new credentials file was imported from the verified
archive; the CI patch omitted it because it was untracked. See the
[generation record](database-credential-generation-evidence.json).

Application checks are pending. The new deleted-record assertion requires the
jshell Gateway SQL test; hosted core tests do not run it.
Do not use this branch as a qualified deployment until those checks pass.
