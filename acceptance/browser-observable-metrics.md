# Browser observable metric limits

Compiler `fe07b0a` supplies common limits for observable metric callbacks.
Hypershell adds no callback wrapper or telemetry transport. The generated browser
package checks values and attributes for single and batch callbacks, bounds
registrations and observations, and restricts batches to selected local
instruments. Callback removal and asynchronous callbacks remain supported.

The compiler correction reproduced two failures before the fix. Five focused
checks and strict TypeScript checks passed after the fix. Full compiler CI is
[run 34993843977](https://github.com/jsell-rh/stego/actions/runs/34993843977).

This candidate updates the compiler pin and generated package. The served console
bundle still needs a CI rebuild from this committed source. Bundle adoption,
matching generation, and rendered application checks are required before this
candidate establishes an application pass. The earlier complete public workflow
used compiler `0b0c932` and does not prove this change.
