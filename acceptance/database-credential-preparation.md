# Common database credential preparation

Gateway and console setup use STEGO's `PrepareDatabaseCredentials` operation.
The common component checks the server identity, stored SQL ownership record,
and SQL names under one resource lock before it returns candidate credentials.
Hypershell selects the resource key and secret fields. STEGO's existing secret
state operation retains the selected candidate before database creation.

The SQL workflow also requires a deleted Gateway's retained SQL record to reject
new application credentials. Existing checks cover separate logical databases,
limited logins, cross-database denial, stored keys, restart, and cleanup.

This branch is a draft. It requires `postgres-client` 1.5.0. The qualified
compiler pin and generated runtime still use the preceding component version.
Compiler release qualification, pin updates, regeneration, and application
checks are pending. Do not use this draft as a qualified deployment.
