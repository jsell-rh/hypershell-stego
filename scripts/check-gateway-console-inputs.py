#!/usr/bin/env python3
"""Check the captured upstream build and common component metadata."""
import hashlib
import json
from pathlib import Path
import sys

project = Path(__file__).resolve().parent.parent
module = project / "gateway-console"
compiler = Path(sys.argv[1]).resolve()
record = json.loads((module / "upstream.json").read_text())
if hashlib.sha256((module / "ui/build.zip").read_bytes()).hexdigest() != record["asset_bundle_sha256"]:
    raise SystemExit("Gateway dashboard assets differ from their checked build")
if (module / "service.yaml").read_text().count("image: " + record["image"]) != 1:
    raise SystemExit("Gateway dashboard image differs from its checked build")
expected = {"postgres-adapter", "otel-tracing", "health-check", "browser-backend", "browser-telemetry", "kubernetes-service"}
actual = {p.name for p in (module / "registry/components").iterdir()}
if actual != expected:
    raise SystemExit("Gateway console component set differs")
for name in sorted(expected):
    relative = Path("registry/components") / name / "component.yaml"
    if (module / relative).read_bytes() != (compiler / relative).read_bytes():
        raise SystemExit("Gateway console component metadata differs from its compiler: " + name)
relative = Path("registry/archetypes/browser-service/archetype.yaml")
common = (compiler / relative).read_text()
expected_archetype = common.replace("  - browser-backend\n", "  - browser-backend\n  - browser-telemetry\n")
if (module / relative).read_text() != expected_archetype:
    raise SystemExit("Gateway console composition differs from the checked browser archetype")
print("Gateway console inputs match their recorded build and compiler.")
