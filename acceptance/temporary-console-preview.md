# Temporary console preview, 2026-09-16

The user requested removal of the preview. The namespace
`stego-preview-20260916-bac5` and its image access RoleBinding are absent.
The public console URL is no longer active.

The API and console images come from source
`6354a23c47f7627b779d637bb0dd3d6e93ca53d0`. That source passed the complete
cluster browser workflow in run `35041052519`. This preview does not qualify
the later common service-account lifecycle migration.

The preview uses the generated console and API, real Keycloak login, separate
PostgreSQL databases for the API and browser sessions, and the in-memory Kafka
fixture. HTTPS verification, PKCE login, the browser session, Gateway creation,
and Gateway retrieval passed through the public console URL. The preview contained one Gateway named
`preview-gateway`. Its temporary database was removed with the namespace.

Gateway workload controllers and the service-account provisioner are disabled.
The placement record is named `Preview - controllers disabled`. It has no
cluster credentials. The release image is an inspection placeholder. Creating
a Gateway changes API data but cannot create infrastructure. This preview is
for console and API inspection, not workload execution or durability testing.

The Pod has no Kubernetes service-account token. All containers run without
root or added capabilities. The namespace quota limits memory to 5 GiB and CPU
to four cores. The Pod has a four-hour deadline. Network policy permits router
access to the console and login service, internal Pod traffic, and cluster DNS.
The event fixture compiled inside the cluster; no workstation build or load
test was used.

Cleanup checked the saved resource IDs before deletion. The operator then
verified that the namespace and image access RoleBinding were absent at
2026-09-16 01:42 UTC. The scheduled cleanup timer is inactive.

Private credentials, manifests, source files, verification results, and cleanup
files are retained under the operator's local directory:
`~/.local/state/stego/previews/stego-preview-20260916-bac5/`.
Credentials are not stored in this repository.
