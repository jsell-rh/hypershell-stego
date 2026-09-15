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
   avoids browser authentication. Verify TLS on all service connections.
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
that contract. The current buffered browser HTTP proxy does not provide it.
