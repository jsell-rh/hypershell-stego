"""Check complete cleanup measurements and the shared container limits."""
import importlib.util
import json
import math
from pathlib import Path
import re
import sys

spec = importlib.util.spec_from_file_location("bounds", Path(__file__).with_name("verify-retained-history.py"))
bounds = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bounds)


def verify(log, state, limits):
    expected = bounds.verify_container(log, state, limits)
    samples = []
    for line in log.splitlines():
        if not line.startswith("BenchmarkGatewayAccountJournalCleanup") or "ns/op" not in line:
            continue
        match = re.fullmatch(r"BenchmarkGatewayAccountJournalCleanup(?:-1)?\s+1\s+(.+)", line)
        if not match:
            raise ValueError("The cleanup result has an unexpected name or count")
        fields = match[1].split()
        if len(fields) % 2:
            raise ValueError("Cleanup metrics are incomplete")
        metrics = {}
        for number, unit in zip(fields[::2], fields[1::2]):
            value = float(number)
            if unit in metrics or not math.isfinite(value) or value <= 0:
                raise ValueError("Cleanup metrics are invalid")
            metrics[unit] = value
        if set(metrics) != {"ns/op", "B/op", "allocs/op", "accounts/op", "journals/op", "provider-deletes/op", "cycles/op", "process-max-rss-B"}:
            raise ValueError("The cleanup metric set is incomplete")
        if metrics["accounts/op"] != 1000 or metrics["journals/op"] != 2000 or metrics["provider-deletes/op"] != 2000:
            raise ValueError("Cleanup did not visit every account and journal")
        if not 30 <= metrics["cycles/op"] <= 40 or metrics["cycles/op"] != int(metrics["cycles/op"]):
            raise ValueError("Cleanup did not retain bounded scan progress")
        if metrics["process-max-rss-B"] > 768 << 20:
            raise ValueError("Process memory exceeds the container limit")
        samples.append(metrics)
    if len(samples) != 3:
        raise ValueError("Three complete cleanup measurements are required")
    return {"samples": samples, "iterations_per_sample": 1, "container_limits": expected,
            "scope": "Generated SQL, encrypted journals, common Keycloak HTTPS client, account audits, provider inventory, scope closure, and store/client reconstruction. The HTTPS server is a protocol fixture. Process RSS includes setup. Real Keycloak capacity, process/database restart, REST/gRPC, other Gateway controllers, and production SLOs are not established."}


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("Usage: verify-cleanup-costs.py RESULT_DIRECTORY")
    root = Path(sys.argv[1])
    try:
        result = verify((root / "benchmark.txt").read_text(),
                        json.loads((root / "container-state.json").read_text()),
                        json.loads((root / "container-limits.json").read_text()))
    except (ValueError, OSError) as error:
        raise SystemExit(str(error)) from error
    (root / "verification.json").write_text(json.dumps(result, indent=2) + "\n")
    print("All three bounded cleanup measurements passed.")
