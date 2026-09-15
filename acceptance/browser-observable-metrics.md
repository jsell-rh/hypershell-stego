# Browser observable metric limits

Compiler `fe07b0a` supplies common limits for observable metric callbacks.
Hypershell adds no callback wrapper or telemetry transport. The generated browser
package checks values and attributes for single and batch callbacks, bounds
registrations and observations, and restricts batches to selected local
instruments. Callback removal and asynchronous callbacks remain supported.

The compiler correction reproduced two failures before the fix. Five focused
checks and strict TypeScript checks passed after the fix. Full compiler CI is
[run 34993843977](https://github.com/jsell-rh/stego/actions/runs/34993843977).

The candidate updates the compiler pin and generated package. Console asset
[run 34994065945](https://github.com/jsell-rh/hypershell-stego/actions/runs/34994065945)
built the bundle from candidate `13363e3`. Its recorded source commit, source
archive hash, compiler pin, and bundle checksum match independent local checks.
The bundle contains 54 entries and 852,525 bytes. The
[build record](browser-observable-metrics-build.json) retains its identity.

The candidate now includes that bundle and its generated backend assets. Both
applications passed repeated generation and drift checks with the pinned
compiler. Full compiler CI passed. Candidate `0c2e7fe` then passed the rendered
browser test in 99.67 seconds with race detection. The console job passed 229
tests, types, architecture checks, lint, bundle reproduction, and generation.
The [rendered record](browser-observable-metrics-rendered.json) retains these
results and their limits. The full parent workflow passed, including core
acceptance with race detection in 1330.563 seconds and all generated image checks.
The manual workflow skipped CNPG and Sandbox; those skips are not passes.
The earlier complete public workflow used compiler `0b0c932` and does not prove
this change.
