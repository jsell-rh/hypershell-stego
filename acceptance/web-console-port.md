# React console port

The generated Go browser backend now serves the captured React UI. The reference
source revision is `14256be29bcfe4fff38bcaf4a41511cb394ea8e1` from
openshift-online/hypershell. Domain views and Gateway probe mapping remain in
Hypershell. Compiler `a1a2ad9` supplies the browser client, session backend,
telemetry providers, deployment settings, and bounded OTLP relay.

The rendered Gateway workflow passed three consecutive runs in the jshell
cluster: 27.02, 25.27, and 24.88 seconds. Chromium 150.0.7871.114 used W3C
WebDriver in a bounded container. No Playwright or workstation browser was used.
The browser trusted only the fixture certificate public keys. The API, browser
backend, identity provider, and collector used TLS.

The workflow used real Keycloak login, selected a managed cluster, and submitted
the Gateway form. It checked the returned IDs and HTTP shape, the committed owner
grant, the delivered event, REST and gRPC access, and denied requests. It then
loaded the page again, restarted both API and browser backend, and used the
existing browser session to retrieve the Gateway. A second user saw an empty
list and could neither create nor retrieve the first user's Gateway. Renewal
and confirmed console and identity-provider sign-out also passed.

The TLS collector received the UI's `gateway.workflow.provision` root span,
its dependency span, the browser backend span, the backend client span, and the
API span as one trace. It received the successful workflow log on that trace
and the `gateway.probes` metric. During a collector fault, all three browser
exports returned bounded failures while Gateway access remained available.
The collector's private error text did not enter process logs.

The rendered checks found two missing behaviors. The API did not resolve the
empty release ID sent by the console. Hypershell now uses explicit
[operator-set catalog defaults](gateway-defaults.md). Common session renewal
also returned temporary failures to concurrent page requests. STEGO now waits
for the active renewal with a two-second limit. A separate test showed that
request cancellation could leave a refresh claim behind. STEGO now removes
that claim with an independent five-second cleanup limit and revokes known
tokens that were not saved. The cancellation test failed before the fix and
passed after it. The earlier failed browser runs remain in the evidence record.

The UI passed 229 tests: 7 probe tests, 164 domain UI tests, and 58 console tests.
It also passed application and test types, import rules, lint, and the production
build. Common configuration tests now belong to STEGO. Its eleven Node telemetry
tests and generated Go backend, TLS collector, and race tests passed. The
inherited React tests still emit `act` warnings.

The asset bundle is `console/ui/build.zip`. STEGO validates and embeds it.
`scripts/check-console-assets.sh` compares the current UI build with that bundle.
The web-console CI job runs this check after the UI build. The application CI
job requires the rendered workflow and saves screenshots and failure details.
The browser backend keeps the existing script policy and adds only escaped
public metadata. Collector addresses and OAuth tokens stay on the server.

The final cluster evidence is in `/tmp/stego-rendered-8ff4d600`; rendered checks
10 through 12 passed with the final compiler pin. The UI check is in
`/tmp/stego-rendered-ui-be8d4987`. These checks prove the creation and retrieval
workflow. They do not prove all console pages, a full accessibility audit,
production credential rotation, or measured deployment capacity. Browser exit
can still lose queued telemetry. The broader STEGO and Hypershell goals remain
active.

The final input-manifest race check passed in 1.053 seconds. All 216 hashes
match two generation passes and the checkout, including the UI bundle and Node
acceptance lockfile. The bundle has 54 files and is 852,497 bytes. The pinned
asset command reproduced it exactly. Both generated browser packages match
the packages used by the UI checks.
