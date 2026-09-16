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
revision = (module / ".stego/compiler-revision").read_text().strip()
expected_config = ("registry:\n  - url: https://github.com/jsell-rh/stego.git\n    ref: " + revision +
                   "\n    path: registry\n  - url: registry\n    ref: application\n")
if (module / ".stego/config.yaml").read_text() != expected_config:
    raise SystemExit("Gateway console must use its pinned common registry and local composition")
if (module / "registry/components").exists():
    raise SystemExit("Gateway console must not copy common component metadata")
relative = Path("registry/archetypes/browser-service/archetype.yaml")
common = (compiler / relative).read_text()
expected_archetype = common.replace("name: browser-service", "name: hypershell-gateway-browser", 1).replace("  - browser-backend\n", "  - browser-backend\n  - browser-telemetry\n")
if (module / "registry/archetypes/hypershell-gateway-browser/archetype.yaml").read_text() != expected_archetype:
    raise SystemExit("Gateway console composition differs from the checked browser archetype")
print("Gateway console inputs match their recorded build and compiler.")
