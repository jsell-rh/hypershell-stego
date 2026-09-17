# Hypershell application composition

This registry contains Hypershell archetypes. Common component declarations
come from the pinned STEGO Git registry in `.stego/config.yaml`. The compiler
and common registry use the same full commit SHA. No common component metadata
is copied here.

`service.yaml` selects public package paths through `component_namespaces`.
Gateway fields, grants, response mapping, and provider policy remain application
code. The common runtime comes from STEGO.

Both consoles use the common browser archetype, which includes browser
telemetry. They do not need local registry sources. Only the API application
archetype remains here. Generated files remain committed for review and repeat
generation checks.

STEGO captures every registry input and checks it again before apply. Repeated
artifact names or input paths across common and local sources are errors.

Registry sources are combined without replacement rules. A local artifact must
have a distinct name; it cannot replace a common artifact. Use application
archetypes to select common components. Keep Gateway policy in application code.

STEGO also permits an explicit `vendor` path for a pinned Git registry. That
checkout must retain its Git metadata and match the selected commit. This
option supplies registry inputs only. Hypershell still fetches and builds its
pinned compiler, so registry vendoring alone does not provide an offline build.
See the [STEGO registry contract](https://github.com/jsell-rh/stego/blob/83592bee5a17de6936cf521b94629e2a225a8d37/specs/registry-composition.md).
