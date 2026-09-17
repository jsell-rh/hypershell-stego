#!/usr/bin/env python3
"""Check the captured upstream build and common browser selection."""
import hashlib
import json
import re
import sys
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

# In CI, also check the module that Go selected for the root application.
if len(sys.argv) not in (1, 2):
    raise SystemExit("Expected at most one selected-module record")
if len(sys.argv) == 2:
    selected = json.loads(Path(sys.argv[1]).read_text())
    name = "github.com/jsell-rh/hypershell-stego/gateway-console"
    matches = re.findall(r"^\s*" + re.escape(name) + r"\s+(\S+)\s*$", (project / "go.mod").read_text(), re.MULTILINE)
    if len(matches) != 1 or selected.get("Path") != name or selected.get("Version") != matches[0] or selected.get("Replace"):
        raise SystemExit("The root application selected a different Gateway console module")
    resolved = Path(selected.get("Dir", ""))
    if not resolved.is_absolute() or not resolved.is_dir():
        raise SystemExit("The selected Gateway console module has no source directory")
    def files(root):
        paths = [root / "go.mod", root / "go.sum", *(root / "out").rglob("*")]
        values = {}
        size = 0
        for path in paths:
            if path.is_symlink():
                raise SystemExit("The Gateway console module has a source link")
            if path.is_dir():
                continue
            if not path.is_file() or path.stat().st_size > 16 << 20:
                raise SystemExit("The Gateway console module has an invalid source file")
            data = path.read_bytes()
            size += len(data)
            values[str(path.relative_to(root))] = hashlib.sha256(data).hexdigest()
            if len(values) > 2048 or size > 64 << 20:
                raise SystemExit("The Gateway console module exceeds the source check limit")
        return values
    expected = files(module)
    if files(resolved) != expected:
        raise SystemExit("The root application module differs from the checked-in Gateway console source")
    print("The selected Gateway console module matches all " + str(len(expected)) + " generated source and module files.")
