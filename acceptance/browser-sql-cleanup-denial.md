The browser Gateway deletion check now removes the provisioning account's
`ADMIN OPTION` on one Gateway login. It preserves the grant's other options
and the other Gateway's permissions. PostgreSQL requires this option to change
that login. See [ALTER ROLE](https://www.postgresql.org/docs/18/sql-alterrole.html)
and [REVOKE](https://www.postgresql.org/docs/18/sql-revoke.html).

A bounded connection as the provisioning account must receive SQLSTATE `42501`
for the role operation. That probe always rolls back. The normal REST deletion
then drives the generated controller and PostgreSQL cleanup path. The ledger
must reach `deleting`, while the API retains both cleanup obligations. Source
Secret data and UID, database OID, login OID, and owner OID must stay unchanged.
The other Gateway must stay ready. The controller must not restore the removed
operator permission.

The fixture restores only the original administrator option, including its
original grantor. Normal controller cleanup must then remove the Gateway's SQL
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
is active. No denied-cleanup recovery result is claimed yet.
