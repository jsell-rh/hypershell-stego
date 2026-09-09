Gateway login identity cleanup uses the STEGO cleanup contract. The service declares
`cleanup_owners: [identity]` on Gateway. The compiler revision is recorded in
[the pin file](../.stego/compiler-revision).

The private state API reads the retained Gateway through generated storage. It
returns the cleanup observation and revision from that row. Only configured
control-plane subjects can read this state or write an observation. The write
requires the current revision and a deleted Gateway. Its deletion notice commits
in the same transaction. Public reads continue to return 404 after deletion.

The identity controller checks the provider on each pass, including after a
recorded success. The Keycloak adapter checks client absence after deletion. A
successful delete response alone does not establish absence. A failed check
requests removal of an earlier confirmation through the conditional API. If
that write fails, a later pass must try again. A stale observation requires a
new state read and new provider work. Unchanged observations do not produce more events.

The application checks cover REST creation and deletion, TLS gRPC observations,
denied subjects and owners, invalid and stale revisions, event rollback and
delivery, API restart, and invalidation after retained inputs change. The real
Keycloak workflow also creates a late client after recorded completion. A new
controller removes it. Unit tests cover a successful delete response that leaves
the client present, reopened observations, and retry after a revision conflict.

Apply the generated migrations and update all API instances before the identity
controllers. A controller refuses deletion when its state response has no
identity cleanup declaration. Older controllers do not record completion.

This owner covers the Gateway login client. Service-account cleanup has its own
existing workflow. Workload and sandbox cleanup do not yet record completion.
A Gateway can change clusters. One global workload flag cannot prove cleanup in
every former cluster. STEGO needs a contract for cleanup obligations by target
before the application can make that claim. Cross-process fencing, owner-specific
subject permissions, safe purge, and production recovery bounds also remain open.
