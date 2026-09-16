#!/usr/bin/env python3
"""Compare declared browser paths with the pinned dashboard's route table."""
import json
import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text()
paths = re.findall(r'\bpath="([^"]+)"', source)
assert paths and paths.count("*") == 1
expected = {"/"}
for route in paths:
    if route == "*":
        continue
    assert route.startswith("/") and "*" not in route
    expected.add(re.sub(r":[A-Za-z][A-Za-z0-9_]*", "{id}", route))
for name in sys.argv[2:]:
    text = pathlib.Path(name).read_text()
    entries = re.findall(r"^    routes: (.+)$", text, re.MULTILINE)
    assert len(entries) == 1
    actual = json.loads(entries[0])
    assert len(actual) == len(set(actual)) and set(actual) == expected
print(f"All {len(expected)} dashboard browser paths match the pinned source.")
