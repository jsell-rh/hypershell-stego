# Per-Gateway console gap

Review date: 2026-09-15.

The complete public Gateway workflow proves the Hypershell management console
and the OpenShell Gateway RPC path. It does not prove the separate OpenShell
console for each Gateway. A disabled console link in that workflow is not
completion evidence for this feature.

The reviewed Hypershell reference is commit
`791fddd0b7d6e779f6bdd17309416d6a516a5a7a`. Its
`components/control-plane/internal/gateway/console.go` creates a separate
console Deployment, Service, exposure resource, network policies, and OAuth
Secret. It reconciles a confidential identity-provider client and uses a
client certificate to connect the dashboard to the Gateway. It publishes the
console address only after the workload and exposure are ready.

The reviewed STEGO variant is commit
`bfba203f97299bb08487346f9e5b99541805629f`, with compiler `b0bd9a4`.
The current sources show these boundaries:

| Source | Current behavior |
| --- | --- |
| `service.yaml`, `internal/httpapi/http.go`, `internal/grpcapi/gateways.go` | The Gateway API preserves `console_address`. |
| `components/web-console/app/adapters/api/gateway-operations.ts` | The management UI reads the address from the API. |
| `packages/gateway-management-ui/src/gateways/gateway-data.ts` | The UI bounds its wait for a console address. |
| `console/service.yaml` | STEGO generates the separate Hypershell management backend and its browser telemetry. |
| `internal/gatewayworkload/kubernetes.go`, `internal/gatewayworkload/controller.go` | The workload controller has no per-Gateway console configuration or reconciliation path. |
| `internal/gatewayidentity` | The identity controller has no per-Gateway console client reconciliation path. |

This is an implementation gap under H1, H2, and H3. API field compatibility and
UI tests do not close it. Do not remove or disable the feature to close the gap.

The next complete console workflow must:

1. Create a Gateway and its console through generated workers. Keep generic
   browser security, telemetry, resource ownership, and reconciliation in STEGO.
   Keep Gateway-specific resource and identity decisions in Hypershell.
2. Publish the controller-owned HTTPS address only after the console, its
   Gateway connection, and its external exposure are verified.
3. Permit the correct owner and granted users. Deny anonymous, forged,
   wrong-audience, and ungranted requests. Preserve Gateway workspace rules.
4. Keep OAuth tokens out of browser JavaScript and prevent direct access that
   avoids browser authentication. Verify TLS between Pods and for external
   services. The upstream dashboard has an HTTP listener. Keep that listener on
   `127.0.0.1` in the same Pod as its generated proxy. Do not expose it through
   a Service or a public listener.
5. Recover from worker and console restart, identity changes, credential
   rotation, and namespace loss. Remove a stale address when service fails.
6. Delete owned console resources and credentials without changes to another
   Gateway. Verify logs, metrics, traces, and repeated generation.

No console implementation or live per-Gateway console result is claimed by
this review. The external DNS and separate Sandbox allocation requirements
also remain open. The historical `acceptance/gateway-workload.md` is explicitly
marked as retired; its deployment-backed database description is not the
current database contract.

The user selected the implementation boundary on 2026-09-15: keep the upstream
OpenShell dashboard. STEGO must generate its common authentication, deployment,
and lifecycle support. Hypershell supplies Gateway-specific configuration and
access rules. Do not port the upstream dashboard backend as part of this work.
The dashboard terminal uses WebSockets; the generated integration must preserve
that contract. The application's current buffered browser HTTP proxy does not
provide it.

STEGO now has an internal local application renderer that reuses its browser
sessions and OAuth flow. The
[session integration CI](https://github.com/jsell-rh/stego/actions/runs/35021067251)
passed at `7d05c2d94f71f44cbee94b44fa9b1b125b8d71ab`, with required PostgreSQL
and race detection. It covers HTTP authentication, header removal, CSRF checks,
restart, refresh, upstream denial, and logout across backend instances. These
checks use a test application server. No service YAML setting enables this
mode yet. That initial revision rejected WebSocket upgrades.

The later common runtime at STEGO revision
`1fc6ac6e2e3dc3160bd39e1aef805e0c8639f994` adds authenticated, bounded WebSocket
delivery. [CI run 35023836717](https://github.com/jsell-rh/stego/actions/runs/35023836717)
passed all four jobs. Its generated session tests require PostgreSQL and use
the race detector. They cover delivery, access denial, logout, expiry, storage
failure, backend close, and runtime stop. The common HTTP lifecycle drains
upgraded handlers, and telemetry records status 101. The generated application
also passed its vulnerability check. These tests use a test application server.

The [STEGO integration record](https://github.com/jsell-rh/stego/blob/a7d1207/specs/upstream-dashboard-integration.md)
lists the remaining work. Hypershell has not yet adopted this mode. Generated
deployment, terminal behavior, and a live upstream dashboard result remain
required. The management-console and Gateway results do not close this gate.
The upstream build also requires root asset paths and runtime style elements
that the current proxy and content policy do not support. The real UI gate must
cover these contracts, including the editor and terminal.

Common Keycloak client management must also come from STEGO. Use the existing
service-account and Gateway identity workflows to prove the
[provider extraction](https://github.com/jsell-rh/stego/blob/b7b3efa/specs/keycloak-provider-boundary.md)
before adding per-Gateway dashboard client policy. Hypershell keeps Gateway
roles, grants, audiences, and ownership identifiers.

STEGO now has initial typed client operations. Its real-Keycloak job passed at
`b6f814165df869797d1418ff1269814f35426924`, including ownership checks,
credential reads, disablement, confirmed deletion, and trace privacy. That test
reconstructs a provider client within one process; it does not restart this
application or Keycloak. Hypershell has not adopted the provider, and its handwritten client has
not been reduced. Moving that client without the policy separation would be
insufficient.

The common service-account creation test also passed at STEGO revision
`84b445767b468ee8b8d388f078fac0220d0b5fc7` in
[run 35026787020](https://github.com/jsell-rh/stego/actions/runs/35026787020).
It creates two clients with different ownership policies and stable provider
IDs. It checks token denial while disabled, creation conflicts, configuration
changes, credential preservation, client reconstruction, and deletion. Container
cleanup passed. All five STEGO CI jobs passed for this revision.

The common service-account profile uses ownership keys under `stego.owner.`.
Hypershell must retain its Gateway and service-account identifiers as policy
values and explicitly migrate existing ownership attributes during adoption.
Native client configuration and verified enablement remain required before the
handwritten client can be removed. The existing application workflows must
then pass with the common provider. This provider test does not close that gate.

STEGO's role mechanisms also passed their real-Keycloak gate at
`96386e526db236aedf3b5dd9503ec66a11d4d9ff` in
[run 35028368052](https://github.com/jsell-rh/stego/actions/runs/35028368052).
All five STEGO CI jobs passed. The tests use two application policies. Client-scoped updates preserve other
roles and groups. Full service-account updates require the owned, disabled
client and its saved subject, and remove excess realm, client, and group access.
Both operations confirm removal before addition and verify the resulting roles.
The shared-user operation does not infer a human identity from missing provider
metadata. Hypershell retains that grant policy. The common role methods have
not yet replaced the handwritten methods in this application.

STEGO revision `261b2ea4be950f6dbfdaa301cfe64098fd3432c1` adds exact scope
reconciliation and typed access-token mappers. The provider detaches shared
scopes without changing their definitions. It confirms removal before addition
and retains mapper IDs when their configuration is correct. Application policy
supplies audiences and claim paths. Small generated tests passed with the race
detector, with and without telemetry. The real Keycloak test passed at
`6360ce42bddda4dd48cc8061abb225dcd1d450c0` in
[run 35030627861](https://github.com/jsell-rh/stego/actions/runs/35030627861).
The test checked exact signed token audiences and roles, identity, lifetime,
optional metadata, mapper repair, preserved shared scope definitions, and
permission denial. It took 45.75 seconds, and container cleanup passed.
Four STEGO CI jobs passed; the compiler job was still running when this result
was recorded. Earlier attempts exposed the assigned-scope response shape and
the separate realm-role scope permission. Those results are retained in the
STEGO provider record. Production permissions did not change.
This application still uses its existing compiler pin and handwritten provider.
No new application result or source reduction is claimed for this change.
