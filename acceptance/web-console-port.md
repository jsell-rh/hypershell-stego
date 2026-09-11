# React console port

The reference React UI is under `components/web-console`, with its Gateway UI
package under `packages/gateway-management-ui`. Its source revision is
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1` from openshift-online/hypershell.
The port includes Gateway list, creation, detail, and service-account pages.
It retains domain use cases, view models, probe definitions, and branding.

The generated STEGO TypeScript client supplies HTTP requests, CSRF handling,
session reads, cancellation, response checks, explicit login navigation, and
safe public error codes. Application adapters map the contract models to the
domain ports. Optional API fields are checked before use. OAuth tokens remain
in the separate generated Go browser backend. Confirmed sign-out ends both
the console and identity-provider sessions.

The port excludes the old Node backend, password-grant development proxy,
old SDK transport, and browser automation files. The production build imports
only the Bash syntax grammar and two required themes. The resulting build has
54 files and uses about 3 MiB on disk. STEGO captured a build in an 835,373-byte
archive without a limit change.

The bounded jshell checks passed application and test types, import boundaries,
lint, 7 probe tests, 164 domain UI tests, 67 console tests, and the production
build. Initial checks found optional-field errors, incorrect migrated test
matchers, and lint errors. These were corrected. The final complete check is
`check3` in `/tmp/stego-ui-final-q0sjvu_6`. The enclosing Job retains its initial
failed result; the later check has a separate successful result. CI now runs
`scripts/check-web-console.sh` with one test worker and a bounded Node heap.

This source port does not replace the served console scaffold yet. These gates
remain open:

- Move common browser telemetry setup, limits, export, and lifecycle into STEGO.
  The copied reference telemetry adapter still contains runtime setup.
- Add the generated authenticated telemetry relay and prove logs, metrics,
  traces, propagation, and collector failure behavior.
- Capture and serve the final UI build through the generated Go backend.
- Prove the rendered Gateway workflow, browser cookie and script policy,
  accessibility, restart, and regeneration.

A build and simulated DOM tests do not prove these browser behaviors. The
existing real PostgreSQL and Keycloak Gateway protocol test remains a separate
required gate. Production deployment, rotation, and capacity also remain open.

The UI passed its type checks and production build again with the exact SDK
files from compiler `440fbbc`. Those files match the pinned generation hashes.
The pinned Gateway protocol workflow then passed in 24.08 seconds, including
provider sign-out. All 155 generation hashes match the second pass and the
checkout. This does not close the browser rendering or telemetry gates above.

Compiler `1d7ae40` adds trace context to each SDK request. The application adapter
passes the current Gateway dependency context and abort signal through this
interface. The generated SDK owns header checks and transport. A reused client
does not retain the previous request's context.

The bounded jshell UI check passed all 239 tests: 7 probe tests, 164 domain UI
tests, and 68 console tests. Application and test types, import boundaries,
lint, and the production build passed. The inherited React tests still emit
`act` warnings; passing tests do not remove the rendered browser gate. The
results are in `/tmp/stego-browser-trace-ui-meu7bs4u`.

The fresh pinned Gateway workflow passed in 13.81 seconds with a TLS OTLP
collector. It checks the parent chain from the supplied browser trace context
through the Go browser backend, its HTTP client, and the API. Login, atomic
Gateway and owner grant creation, access rules, REST and gRPC, event delivery,
restart, renewal, and provider sign-out also passed. The input-manifest race
test passed. All 155 output, state, and dependency hashes match two generation
passes and the checkout. The UI used the same generated SDK bytes. These
results are in `check3` under `/tmp/stego-browser-trace-t39scx2c`.

This proves propagation through the application. It does not prove export of
the browser's root span. The common browser telemetry runtime, authenticated
relay, and rendered workflow remain the next required work.
