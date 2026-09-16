# Service-account provider recovery state

The private `ServiceAccountProviderStateService` stores encrypted recovery
records for the service-account provisioner. STEGO supplies the versioned SQL
state store, encryption, authenticated journal, RPC transport, and exact grant
policy. Hypershell supplies the Gateway and account IDs and the allowed caller.
This state does not change domain roles, account status, expiry, or events.

The API requires both a configured control-plane subject and an exact grant in
`HYPERSHELL_PROVIDER_STATE_GRANTS`. The grant uses the verified issuer and
subject, resource `ServiceAccount`, operation `provider-state`, and an empty
target. Administrator roles, Gateway ownership, cleanup grants, and controller
write grants do not substitute for this grant. The machine caller does not need
a username or other profile fields. These RPCs do not create user or grant rows.

The record key is entity `ServiceAccount`, the canonical account ID, and scope
`gateway:<Gateway ID>`. The journal's authenticated encryption also binds the
operator's instance ID. Normal work requires a retained live Gateway and an
account reservation with matching Gateway and public client IDs. Its status
must be `provisioning`, `ready`, or `degraded`. Cleanup can retain state for a
missing account row, but the Gateway must still exist in retained storage.
An account row that belongs to another Gateway is always denied.

A journal save uses an independent serializable transaction and an exact state
version. It does not acquire the Gateway row lock. The API can therefore retain
its existing lock during a provisioner call while the journal commits before
the next provider effect. The journal transaction must not write domain rows or
run user-profile projection. Recovery state does not authorize a Keycloak
operation; the private provisioner caller and application policy do that.

Records are limited to 60 KiB, including the encryption envelope. The API checks
framing but does not hold the encryption key. The provisioner uses STEGO's
`StateJournal` to authenticate each loaded record and each save result. A lost
save acknowledgement stops work. Version conflicts are not retried within the
call. No state deletion method is exposed. The application adapter is
`serviceaccountprovisioner.NewProviderStateJournal`.

`TestServiceAccountProviderStateAcrossLockAndRestart` is part of the required
jshell API gate. It creates Gateways through REST, stores an account reservation,
and holds a real PostgreSQL Gateway row lock while a journal RPC commits. It
then checks API restart, ciphertext storage, denied reads and writes, stale
versions, malformed requests, account isolation, and retained orphan cleanup.
It also checks that journal requests do not change account metadata or emit
domain events. The live result is pending.

The authorization tests passed with the race detector in 1.021 seconds. The
application and acceptance packages compile, and generation has no drift.
The production provisioner does not yet use this journal. Its service-account
lifecycle adoption and complete Keycloak workflow remain required.
