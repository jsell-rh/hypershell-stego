Gateway declares `identity_users/GrantsSynchronized` through STEGO's existing
condition contract. The condition describes a completed scan of stored Gateway
grant references, including retained removals. It is separate from
`identity/ClientReady` and from overall Gateway health.

A complete, clean cycle records `True/GrantSyncComplete`. A failed action records
`Unknown/GrantSyncIncomplete`. The failure survives partial passes and controller
replacement. A clean tail cannot erase an earlier failure. A later full, clean
cycle can restore True. A successful partial rescan preserves the last complete
observation for the same inputs; it does not cause repeated status changes.
Messages are fixed text. Provider error text is not stored.

A grant create or delete records `Unknown/GrantsChanged` and resets the scan
checkpoint in the grant/event transaction. Even an already empty checkpoint
advances its version. An older pass cannot restore positive evidence after that
change. This invalidation leaves the client condition intact. Desired Gateway
changes invalidate both conditions through the generated desired generation.
The application keeps provider issuer and subject fixed; profile names and
email addresses do not select the provider identity.

Cycle load returns the resource revision read before work. Save requires that
revision, the desired generation, and the checkpoint version. The API checks all
three while it holds the live Gateway lock. It commits checkpoint, condition,
and event changes together. A failed event rolls back the whole transaction.
A repeated identical condition emits no new event. After a condition write,
the response contains the resulting resource revision. A partial cursor save
without a condition change still preserves the public revision.

This is evidence of an observation, not proof of controller liveness or a maximum
observation age. It covers stored grant references. It does not certify unrelated
provider identities or changes made through direct SQL. It does not revoke tokens
that were already issued. Provider fencing, observation-age policy, and production
recovery capacity remain open requirements.

For this upgrade, stop the old identity controller, apply the current
`out/storage/migrations/000007_resource_conditions.sql`, start the new API, then
start the new controller. The condition declaration changes the verified resource
trigger. The migration advances the desired generation and preserves old condition
history. Startup rejects the previous trigger. The new cycle API requires a
resource revision; older cycle save requests are rejected. The new controller
rejects an API without the required condition or revision contract before provider
work. This procedure does not establish an upgrade without downtime.

The provider-timeout regression first failed in 21.188 seconds because the
recovery API had no grant condition. The new workflow keeps that failure across
API and controller restart. It requires Unknown after the resumed failed cycle
and True only after another full clean cycle. Other tests cover denied and stale
writes, loss of the required resource revision, checkpoint-only invalidation,
condition/event rollback, and independent condition owners.

The real Keycloak workflow checks conditions after owner and viewer synchronization,
a profile rename, role removal, and restart. Offline grant removal must change
the condition to Unknown before the controller restarts. The API restart must
preserve that invalidation and transition time. The repaired provider roles and
a new complete scan restore True. Existing token-validity checks remain explicit.
These five application workflows passed in 95.137 seconds under race detection.

The upgrade test uses the exact condition migration from application
`0128ef569be258a92906cf6da0ab6d32625b54f4`, retained in
`testdata/pre_grant_conditions.sql`. It publishes a client condition under that
contract, rejects it at new-store startup, applies the new migration, and checks
history, generation invalidation, the new Unknown condition, and repeated apply.
The upgrade and transaction tests passed together in 6.514 seconds.

The full `go test -race -count=1 -timeout=18m ./...` run passed with PostgreSQL
and Keycloak required. The acceptance package completed in 923.127 seconds.
Static checks passed. Kubernetes and VM workload gates were not repeated locally
for this condition change. All six remote jobs for the preceding scan-cycle
commit `0128ef569be258a92906cf6da0ab6d32625b54f4` passed in CI run 34517913592;
that earlier run does not cover this condition change.

Final controller compatibility tests passed in 1.305 seconds under race detection.
They reject a missing grant owner, missing or nil condition, and missing cycle
revision before provider work. Vet passed again. Pinned regeneration preserved
all 90 output, state, and dependency file hashes.
