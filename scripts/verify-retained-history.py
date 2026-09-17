"""Require complete bounded measurements. This does not set a production SLO."""
import json
import math
from pathlib import Path
import re
import sys


def verify_container(log, state, limits):
    if len(log.encode()) > 1 << 20 or "PASS" not in log.splitlines():
        raise ValueError("The benchmark did not finish")
    if "FAIL" in log or "SKIP" in log:
        raise ValueError("A failed or skipped benchmark cannot qualify")
    if (state.get("Status") != "exited" or state.get("ExitCode") != 0
            or state.get("Running") is not False or state.get("OOMKilled") is not False
            or state.get("Dead") is not False or state.get("Error") != ""):
        raise ValueError("The test container did not exit successfully")
    expected = {"NanoCpus": 1000000000, "Memory": 768 << 20,
                "MemorySwap": 768 << 20, "PidsLimit": 128,
                "Privileged": False, "ReadonlyRootfs": True, "NetworkMode": "host"}
    if any(limits.get(key) != value for key, value in expected.items()):
        raise ValueError("The test container limits differ from the contract")
    if limits.get("CapDrop") != ["ALL"] or limits.get("CapAdd"):
        raise ValueError("The test container has unexpected capabilities")
    if "no-new-privileges" not in limits.get("SecurityOpt", []):
        raise ValueError("The test container can gain privileges")
    return expected


def verify(log, state, limits):
    expected = verify_container(log, state, limits)
    rows = {10000: [], 100000: []}
    for line in log.splitlines():
        if not line.startswith("BenchmarkRetainedGrantInventory/"):
            continue
        # Go can print a case name before it runs. Only result lines have metrics.
        if "ns/op" not in line:
            continue
        match = re.fullmatch(r"BenchmarkRetainedGrantInventory/rows-(10000|100000)(?:-1)?\s+3\s+(.+)", line)
        if not match:
            raise ValueError("The benchmark result has an unexpected name or count")
        fields = match[2].split()
        if len(fields) % 2:
            raise ValueError("The benchmark metrics are incomplete")
        metrics = {}
        for number, unit in zip(fields[::2], fields[1::2]):
            value = float(number)
            if unit in metrics or not math.isfinite(value) or value <= 0:
                raise ValueError("The benchmark has invalid metrics")
            metrics[unit] = value
        if set(metrics) != {"ns/op", "B/op", "allocs/op", "pages/op", "rows/op", "process-max-rss-B"}:
            raise ValueError("The benchmark metric set differs from the contract")
        size = int(match[1])
        if metrics["rows/op"] != size + 1 or metrics["pages/op"] != size // 100 + 1:
            raise ValueError("The benchmark did not read the complete history")
        if metrics["process-max-rss-B"] > 768 << 20:
            raise ValueError("The reported process memory exceeds its container limit")
        rows[size].append(metrics)
    if any(len(samples) != 3 for samples in rows.values()):
        raise ValueError("Three measurements are required for each history size")
    return {"samples": rows, "iterations_per_sample": 3, "container_limits": expected,
            "scope": "Complete SQL cursor scans with domain authorization and row validation. Provider calls, transport, and complete controller recovery are excluded. Process RSS is a high-water mark that includes setup and earlier cases. Hosted-runner timings do not establish a production SLO."}


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("Usage: verify-retained-history.py RESULT_DIRECTORY")
    root = Path(sys.argv[1])
    try:
        result = verify((root / "benchmark.txt").read_text(),
                        json.loads((root / "container-state.json").read_text()),
                        json.loads((root / "container-limits.json").read_text()))
    except (ValueError, OSError) as error:
        raise SystemExit(str(error)) from error
    (root / "verification.json").write_text(json.dumps(result, indent=2) + "\n")
    print("All six bounded retained-history measurements passed.")
