# Hypershell web console

This directory contains the reference React UI port. Gateway pages, view models,
role hints, and domain probes belong to Hypershell. The generated STEGO client
supplies requests, session CSRF handling, response checks, and cancellation.
The separate Go backend owns OAuth tokens and browser sessions.

Source: openshift-online/hypershell commit
`14256be29bcfe4fff38bcaf4a41511cb394ea8e1`. The port retains the domain UI package
and branding. It excludes the Node backend and the development password-grant
proxy. See the repository license for the source license.

The UI port is under test. Asset capture, content security policy, common
browser telemetry, and a rendered Gateway workflow must pass before this
replaces the console scaffold. Do not treat a successful build as delivery.

CI installs the locked workspace with package scripts disabled. It runs
`scripts/check-web-console.sh` with one test worker and a bounded Node heap.
Run these checks in CI or a dedicated cluster Job. The check covers types,
import rules, lint, domain and page tests, and the production asset build.
It does not run a browser automation tool.
