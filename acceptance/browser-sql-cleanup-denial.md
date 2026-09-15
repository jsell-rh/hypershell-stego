The browser Gateway deletion check removes `INHERIT` and `SET` access to one
Gateway's database-owner role from the provisioning account. It records each
original grant and grantor, then removes only these options in one transaction.
It keeps `ADMIN OPTION` and the other Gateway's permissions. Database changes
require owner access. See [ALTER DATABASE](https://www.postgresql.org/docs/18/sql-alterdatabase.html)
and [REVOKE](https://www.postgresql.org/docs/18/sql-revoke.html).

A bounded connection as the provisioning account must receive SQLSTATE `42501`
for `ALTER DATABASE ... ALLOW_CONNECTIONS false`. That probe always rolls back. The normal REST deletion
then drives the generated controller and PostgreSQL cleanup path. The ledger
must reach `deleting`, while the API retains both cleanup obligations. Source
Secret data and UID, database OID, login OID, and owner OID must stay unchanged.
The other Gateway must stay ready. The controller must not restore the removed
operator permission.

The fixture restores only the original owner-access options and grantors in
one transaction. Normal controller cleanup must then remove the Gateway's SQL
and namespaces before the existing workflow can pass. A cleanup handler also
restores the permission if the test stops early.

This is an application check of the existing common runtime. It adds no
production permission or controller code. Its public evidence contains the
denial code, pending state, one Secret UID, and comparison results. It contains
no credentials or Secret contents. The evidence file describes the blocked
phase; a complete workflow pass is also required to prove later cleanup.

Formatting, source checks, and the complete acceptance package build passed.
The [API run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937319123)
passed all 30 required tests with matching source and generation records and
complete cleanup. It does not execute this new live denial case. The
[browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34937319194)
failed during permission setup, before the denial assertion. The old setup
tried to remove `ADMIN OPTION`; PostgreSQL rejected it because dependent grants
exist. The Job failed and host cleanup passed. The revised setup avoids that
grant dependency. No denied-cleanup recovery result is claimed yet.
