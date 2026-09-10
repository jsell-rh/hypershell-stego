# Database controller write permissions

Conditional database patches require STEGO's generated exact grant policy.
The operation is `observe.provider`, the resource is `ManagedDatabase`, and the
target is the current stored provider name. The supported provider names are
`deployment` and `cnpg`. An example API setting is:

```sh
HYPERSHELL_CONTROLLER_WRITE_GRANTS='[{"issuer":"https://issuer.example","subject":"database-controller","resource":"ManagedDatabase","operation":"observe.provider","target":"deployment"}]'
```

The caller also needs a verified token, a subject in
`HYPERSHELL_CONTROL_PLANE_SUBJECTS`, and the current resource revision. Missing
grants deny the write. Cleanup grants and Gateway grants do not authorize
database observations. An administrator role does not replace these checks.
Grant changes require restart of every API instance.

The controller can update `status`, `connection_secret`, or both. Empty patches
and patches with only desired settings are denied. A patch that mixes observation
fields with other fields is invalid. The API checks the stored provider and the
field set inside the mutation transaction, before applying changes. The request
cannot change its provider to select another grant. The provider name is already
immutable under the catalog contract.

The provider name scopes this grant within one API deployment. It does not
identify a Kubernetes cluster, fence a provider process, or restrict the grant to
one database ID. Provider credentials and stable provider identity still need a
complete contract.

These API grants control stored observations. They do not revoke provider
credentials or undo provider work that occurred before a denied status write.

`TestDatabaseControllerWriteGrantsAcrossProvidersAndRestart` creates both provider
types through REST. It checks wrong subjects, wrong providers, wrong operations,
cleanup-only access, missing grants, unconfigured subjects with explicit grants,
administrator roles, and invalid field groups. Denied writes must leave the
whole row and committed database event count unchanged. A test trigger records events
after insertion, so outbox delivery cannot hide an extra committed event.

Successful observations must advance one revision, store the requested fields,
commit one event, and deliver it through the generated runtime. A REST request
cannot bypass the revision requirement. The test removes a grant, restarts the
API, and requires denial with the same token. The other provider remains able
to publish its authorized observation.

The baseline failed in 3.21 seconds: an identity controller without a database
grant could publish database status. The focused permission check passed in
5.48 seconds after the fix. The existing database revision test also passed;
it checks stale observations, malformed preconditions, event rollback, and
restart. The catalog workflow now requires denial when a controller tries to
change the desired engine version, then proves that an administrator can make
that change. The Gateway grant regression also passed.

The real database Kubernetes workflow passed on 2026-09-10 with the explicit
provider grant. Provisioning, TLS access, persistence, foreign namespace denial,
offline deletion, and late-effect cleanup took 64.33 seconds. Replay under C and
ICU ordering took 12.18 seconds. The acceptance package took 77.547 seconds.
These tests verify the permission change; they do not establish production
capacity.

The complete Gateway Kubernetes gate passed in 213.285 seconds. Deletion before
workload startup took 51.27 seconds. The live database and identity workflow took
160.97 seconds. It verified owner and viewer access, denied writes, provider
persistence, Pod and database restart, namespace replacement, offline deletion,
and cleanup of late resources on the former cluster.

The complete PostgreSQL/Keycloak race suite passed with a 664.210-second
acceptance package run. It includes the new permission test, stale-observation
rejection, transaction rollback, event delivery, and restart. Module verification,
formatting, and `go vet` also passed.

STEGO owns grant parsing and exact matching. Hypershell supplies the field set,
operation name, provider target, and transaction call. No compiler change is
required for this policy.

Public administrator access to status and the connection-secret reference is
still part of the current API. The connection-secret ownership decision remains
pending. This change does not complete database field ownership or generation
tracking. Other catalog operations, private reads, controller creation and
deletion, provider credentials, and cross-process fencing remain open work.
