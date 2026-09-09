The Gateway identity controller now maps stored user grants to Keycloak client
roles. The application test completes a real browser login and a PKCE S256 code
exchange. It then verifies the issued access token with STEGO's generated
verifier and the provider's trusted keys.

An owner grant supplies `openshell-admin` and `openshell-user`. A viewer grant
supplies `openshell-user`. The controller reads current grants before each
provider update. It changes only the user's roles for the managed Gateway client.
It preserves roles for other clients and the realm. It removes excess roles
before it adds roles, then checks effective roles through the provider API.

The provider subject is the stored, verified subject. The issuer must match the
configured Keycloak realm exactly. Profile names and email addresses are not
identity keys. A service-account user cannot enter this human role update path.
An unbound legacy user needs a trusted migration; the controller does not infer
the identity from a profile.

The real login test exposed a missing subject mapper. The managed Gateway client
had audience and role mappers, but browser access tokens had no usable subject.
Service-account token tests did not prove browser behavior. The client now has
a dedicated `oidc-sub-mapper`. STEGO still requires a valid subject. Its signature,
issuer, audience, and expiry checks did not change. The mapper uses the provider
user ID, as defined in the
[Keycloak subject mapper](https://github.com/keycloak/keycloak/blob/26.7.3/services/src/main/java/org/keycloak/protocol/oidc/mappers/SubMapper.java).

Two private generated gRPC methods support this workflow. One lists retained
grant references in pages of 100. The other returns the current role and verified
identity for one user and Gateway. Only configured control-plane subjects can
call them. Deleted grants remain in the inventory so that restart can remove
roles for a missed deletion. A missing or denied response never supplies an
empty role as a substitute for known state.

Each controller pass retains its page position when its time budget expires.
Later users can then receive service on a later pass. A complete scan starts
again on the next cycle. The current limit is 10,000 retained grant references
per Gateway. A larger inventory causes an error. This is a resource limit, not
a production capacity result. Use one active identity controller. Distributed
coordination and larger inventories remain open.

| Behavior | Evidence |
| --- | --- |
| Real browser login and PKCE | `TestGatewayUserLoginFollowsStoredGrants` uses provider forms, session cookies, a state check, a code verifier, and verified TLS |
| API identity and role | A real provider token creates a Gateway through REST |
| Gateway owner and viewer roles | Fresh Gateway tokens reflect grants made through the REST API |
| Role union | Removing an owner grant retains access supplied by a viewer grant |
| Profile change and reuse | Renaming a user preserves access; a new subject with the old profile name receives no access |
| API audience isolation | A Gateway token is rejected by the API and by verification with the API audience |
| Restart | A removal made while the controller is stopped clears provider roles after both processes restart |
| Current state and denied reads | Domain and controller tests reject untrusted readers and missing or mismatched state |
| Provider boundaries | Tests check the issuer, managed Gateway binding, subject lookup, target client, and failed removal before additions |
| Scan progress | A canceled pass resumes with the next user instead of repeating the first user |

The test creates the realm, API login client, users, and API creator role as
fixture setup. The application creates Gateway clients and maps their user roles.
The fixture does not assign the tested Gateway roles. Usernames can change in
this test realm so that the identity check covers that provider configuration.

This workflow proves role changes in newly issued tokens after the controller
applies the change. It does not invalidate an already issued token. The test
confirms that an old token retains its earlier role claims. Tokens currently
have a five-minute lifetime. The user was asked whether grant removal requires
an online Gateway access check. That decision and the corresponding enforcement
remain open.

The controller checks and writes provider state through separate requests.
Provider updates can lag the database, and concurrent changes or uncertain
provider responses require another pass. This workflow is not a proof of
immediate revocation, multiple-controller safety, or production capacity.
Gateway workload deployment, device login, full user and role APIs, and provider
key rotation for the API remain part of the broader goal.

The full local race suite passed with PostgreSQL and Keycloak required. The
acceptance package took 280.953 seconds. A later timeout correction passed the
controller and provider race tests. The correction preserves the retry position
when the last user in a page times out. Dependency verification and regeneration
also passed.

A local benchmark read one user's current Gateway access 100 times with 10,000
unrelated users and grants. It averaged 0.928 ms, 46,462 bytes, and 612 allocations
per call. It used Go 1.26.8, PostgreSQL 18.6, and an Intel Core Ultra 9 185H.
This includes the domain transaction and database reads. It excludes gRPC,
Keycloak, browser login, controller scans, and concurrent load. Run
`go test -run '^$' -bench '^BenchmarkGatewayIdentityUserState$' -benchtime=100x ./acceptance`
with the PostgreSQL test settings.

The final focused race run passed after the timeout fix. It repeated the real
login workflow and the current-state checks in 29.032 seconds.

The login test now discovers owner and viewer role IDs through the authenticated
[role catalog](role-catalog.md). Recipient user ID discovery still uses the test
database. The public user-discovery policy remains open.
