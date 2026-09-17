#!/usr/bin/env python3
"""Check the captured upstream build and common browser selection."""
import hashlib
import json
from pathlib import Path

project = Path(__file__).resolve().parent.parent
module = project / "gateway-console"
record = json.loads((module / "upstream.json").read_text())
if hashlib.sha256((module / "ui/build.zip").read_bytes()).hexdigest() != record["asset_bundle_sha256"]:
    raise SystemExit("Gateway dashboard assets differ from their checked build")
if (module / "service.yaml").read_text().count("image: " + record["image"]) != 1:
    raise SystemExit("Gateway dashboard image differs from its checked build")
revision = (module / ".stego/compiler-revision").read_text().strip()
expected_config = ("registry:\n  - url: https://github.com/jsell-rh/stego.git\n    ref: " + revision +
                   "\n    path: registry\n")
if (module / ".stego/config.yaml").read_text() != expected_config:
    raise SystemExit("Gateway console must use its pinned common registry")
if (module / "registry").exists():
    raise SystemExit("Gateway console must not copy the common browser composition")
if (module / "service.yaml").read_text().splitlines().count("archetype: browser-service") != 1:
    raise SystemExit("Gateway console must select the common browser archetype")
print("Gateway console inputs match their recorded build and compiler.")
