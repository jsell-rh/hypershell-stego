# React console port

The reference React UI is under `components/web-console`, with its Gateway UI
package under `packages/gateway-management-ui`. Its source revision is
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1` from openshift-online/hypershell.
The port includes Gateway list, creation, detail, and service-account pages.
It retains domain use cases, view models, probe definitions, and branding.

Compiler `e4ca4dc` supplies the common browser client and telemetry runtime.
The generated client owns HTTP requests, CSRF, session reads, cancellation,
validation, explicit login, and declared public error codes. The generated
telemetry package owns providers, batching, limits, export, random trace IDs,
and lifecycle. Gateway adapters map domain probes to spans, logs, and metric
attributes. OAuth tokens remain in the separate Go browser backend. Confirmed
sign-out ends both the console and identity-provider sessions.

The Go backend relay requires an active session, same Origin, CSRF token, and
bounded protobuf body. It limits each session's request rate. The shared Go
runtime validates telemetry and uses its verified TLS collector connection.
Browser input cannot set the exported service or scope identity. Collector
addresses and credentials stay on the server.

The pinned Gateway workflow passed in 12.03 seconds. A TLS OTLP collector
received the browser root span, the backend and API parent chain, a browser
log with the same trace ID, and a metric with the expected service identity.
The workflow also passed atomic Gateway and owner grant creation, access rules,
filtered lists, denied requests, REST and gRPC, event delivery, restart, renewal,
and provider sign-out. The input-manifest race check passed in 1.055 seconds.
All 162 hashes match two generation passes and the checkout: 161 generated,
state, and Go dependency files plus the Node acceptance lockfile. The installed
acceptance telemetry package is checked against the generated package bytes.
The final pinned check is `check3` in `/tmp/stego-browser-relay-1orkdb7y`.

The UI passed 233 tests: 7 probe tests, 164 domain UI tests, and 62 console tests.
Generic provider tests moved to STEGO, where ten runtime tests now cover all
three signals, failure, limits, and lifecycle. The UI also passed application
and test types, import checks, lint, and its production build. Its domain test
checks export of Gateway spans, logs, and metrics without the correlation ID
as an attribute. The inherited React tests still emit `act` warnings.

The first UI check stopped at lint. It found a test stub without an await and
lifecycle callback types that did not state independence from `this`. The test
and common declaration were corrected. The final UI check is `check2` in
`/tmp/stego-browser-otel-ui-codwet_4`; the enclosing Job retains the initial
failed result. Both generated browser packages match the final pinned files.
The 54-file build was captured in an 852,377-byte ZIP without a limit change.
The build contains the generated telemetry runtime.

The source port does not replace the served scaffold yet. These gates remain:

- Check collector failure through the complete browser application.
- Complete the common browser configuration path for deployment settings.
- Serve the final captured build through the generated Go backend.
- Prove the rendered Gateway workflow, cookie and script policy, accessibility,
  restart, and regeneration in a real browser.
- Prove production deployment, credential rotation, and measured capacity.

A build and simulated DOM tests do not prove browser policy or rendering.
The bounded SDK queues do not guarantee delivery after a page closes.
The broader Hypershell and STEGO goals remain active.
