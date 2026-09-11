# Generated TypeScript client acceptance

The service declaration selects STEGO's `typescript-sdk` component. It captures
the same reference OpenAPI files as the Go SDK. The output in `out/tssdk`
contains the browser module, request and response types, and package metadata.
No trex generator or npm runtime dependency is needed for this client.

STEGO owns same-origin HTTPS requests, session CSRF handling, cancellation,
request and response limits, fixed errors, and runtime contract checks. The
application owns its UI and domain mappings. The application has no production
transport wrapper around this client.

The real browser Gateway test logs in two users through Keycloak. A Node test
fixture supplies browser cookies and Origin headers to native fetch. The
client receives no cookie or OAuth configuration. The fixture checks that
requests stay on the console origin and that the client supplies no bearer or
cookie header. This proves the protocol, not a real browser's cookie policy.

The generated module creates a Gateway, retrieves it, updates its name, and
finds it in the owner's list. Another user receives 404 for retrieval and 403
for creation. That user's filtered list is empty. The Go fixture then checks
the owner grant and receives the creation event through the generated runtime.
The complete test also checks REST and gRPC access, console process restart,
token renewal, and sign-out. The existing Gateway tests retain their transaction
rollback and denied-request coverage.

`browser_sdk_types.mts` checks the actual generated declarations with the locked
TypeScript 6.0.3 compiler. It includes valid calls and rejected argument types.
CI installs this test dependency with npm scripts disabled. Node.js 24.18.1
runs the protocol fixture. The SDK has no runtime dependency on these test files.

The first combined protocol check passed in 20.50 seconds. Compiler review
then added separate request model types and stricter schema checks. Both the
common tests and the Hypershell declaration check passed after those changes.
The fresh pinned check is recorded below.

The reference React UI still needs to import this client and map its calls and
models. The console page remains a scaffold. Asset builds, browser telemetry,
rendered page checks, and production deployment remain open. The Node fixture
is test code and must not be used as a production browser transport.

The fresh check used published compiler `cc35051`. The Gateway browser workflow
passed in 20.79 seconds (21.831 seconds for the package). The input-manifest
race test passed in 1.057 seconds. The actual Hypershell declarations passed
the TypeScript check. All 155 generated, state, and dependency hashes matched
two pinned generation passes and the local checkout. Both input manifests and
the tested application source match the checkout. Later edits affect records
only. The cluster Job completed.

The source archives, logs, results, and hashes are in
`/tmp/stego-ts-sdk-pin-iw1a51yw`. The initial test record is in
`/tmp/stego-ts-sdk-_cqhke3j`. Both namespaces were removed. Full compiler CI
passed for `cc35051`. Added common cancellation and deadline checks passed
in the cluster in 3.383 seconds. Full CI for this application revision remains
a separate check. This result does not close the remaining UI or enterprise
requirements.

Compiler `440fbbc` supplies SDK 1.1 and browser-backend 1.2. The SDK adds explicit
login navigation and declared public API error codes. Login retains the path
and query and omits URL fragments to match the backend contract. Hypershell
declares `service_account_name_exists`; private error bodies remain hidden.

The fresh pinned PostgreSQL and Keycloak Gateway workflow passed in 24.08
seconds (25.124 seconds for the package). The input-manifest race test passed
in 1.055 seconds. All 155 generated, state, and dependency hashes match both
generation passes and the checkout. Results are saved in
`/tmp/stego-ui-pin-0lefryw_`. Full compiler CI passed for this revision.
