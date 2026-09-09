Authenticated users can now list and retrieve roles through the application API.
The Gateway login test finds owner and viewer role IDs through REST before it
creates grants. This removes direct database access for role discovery from
that workflow. No compiler change was required.

The routes are `GET /api/hypershell/v1/roles` and
`GET /api/hypershell/v1/roles/{id}`. They return the reference role fields,
including names, descriptions, permission maps, and the built-in flag. Lists
support search, ordering, paging, and count-only requests. Authentication is
required; a Gateway grant is not required to read this catalog. Catalog access
does not give the caller authority to create a Gateway or assign platform roles.
The application exposes no role mutation route.

Apply `migrations/000004_role_catalog.sql` before the API starts. Use the database
connection settings for the application database and run:

```sh
psql --set=ON_ERROR_STOP=1 --file=migrations/000004_role_catalog.sql
```

The migration adds role metadata and seeds `platform:admin`, `gateway:creator`,
`gateway:owner`, and `gateway:viewer`. Existing roles keep their IDs and creation
times. Existing grants keep their references. New built-in roles have fixed
KSUIDs. Custom roles remain unchanged. A repeat run repairs changed built-in
metadata but does not change timestamps when the metadata is current. A deleted
built-in role causes the whole migration to fail. The migration does not restore
that role or its access as a side effect.

The permission maps describe the reference catalog. Domain handlers still enforce
authority and resource scope. A catalog entry alone cannot grant access. Global
grant projection remains open.

| Check | Evidence |
| --- | --- |
| Reference response | The generated process returns the role list against the pinned OpenAPI schema |
| Authentication | Missing and forged tokens fail on both routes; an authenticated user without roles can read the catalog |
| Query bounds | Invalid fields, duplicate parameters, invalid pages, and oversized pages fail |
| Access limits | A catalog reader cannot create a Gateway; an owner cannot assign discovered platform roles to a Gateway |
| Upgrade | Existing role IDs and owner grants survive the metadata migration |
| Repeat migration | Current metadata keeps its timestamp; changed metadata is repaired; custom roles remain unchanged |
| Deleted roles | A deleted built-in role stops the migration and rolls back all changes |
| Invalid metadata | The database rejects non-object permissions; corrupt stored metadata causes a complete API failure |
| Restart | Role IDs remain stable after the generated application restarts |
| Browser workflow | Real Keycloak login uses REST role discovery before grant creation |

Run `scripts/check-gateway.sh` with the required PostgreSQL test settings. It
checks dependencies, pinned regeneration, and the full race suite with PostgreSQL
and Keycloak required. The focused role and browser-login race checks passed in
33.152 seconds. The full local race suite also passed; the acceptance package
took 295.363 seconds. Dependency verification passed.

Sparse fields and REST page sizes above 100 remain open. The reference has no
public user-discovery route. The browser workflow now obtains the recipient ID through the
[self-identity route](current-user.md). A searchable user directory remains a
separate policy decision. Production capacity has not been measured for the
role catalog.
