# Hypershell application composition

This registry contains Hypershell archetypes. Common component declarations
come from the pinned STEGO Git registry in `.stego/config.yaml`. The compiler
and common registry use the same full commit SHA. No common component metadata
is copied here.

`service.yaml` selects public package paths through `component_namespaces`.
Gateway fields, grants, response mapping, and provider policy remain application
code. The common runtime comes from STEGO.

The management console uses the common browser archetype. The Gateway console
has a separate local archetype that adds browser telemetry to its upstream
dashboard integration. Generated files remain committed for review and repeat
generation checks.

STEGO captures every registry input and checks it again before apply. Repeated
artifact names or input paths across common and local sources are errors.
