# Hypershell Gateway UI

This private workspace package contains the Gateway list, creation, detail,
and service-account pages. It also contains domain view models, use cases,
ports, and probe definitions. The source comes from the reference Hypershell
commit recorded in the web console README.

The host supplies Gateway operations and navigation through `GatewayUiProvider`.
It maps the `GatewayControlPlane` port to the generated STEGO SDK. The host also
supplies the React Intl and TanStack Query providers. The package does not own
browser sessions, OAuth tokens, API transport, routing, or deployment.

The package is used as source within this workspace. It is not a public npm
package. Run its tests through `scripts/check-web-console.sh` in CI or a
bounded cluster Job.
