#!/usr/bin/env bash
# Run only in the bounded hosted CI unit. Never use this on a developer host.
set -euo pipefail
result=${1:?result directory is required}
test "${STEGO_CAPACITY_CI:-}" = 1
test "${GITHUB_RUN_ID:-}" != ''
python3 - "$result/cgroup-limits.json" <<'PY'
import json
import pathlib
import sys

record = pathlib.Path('/proc/self/cgroup').read_text().strip().splitlines()
if len(record) != 1 or not record[0].startswith('0::/system.slice/stego-capacity-'):
    raise SystemExit('A dedicated capacity cgroup is required')
root = pathlib.Path('/sys/fs/cgroup') / record[0][3:].lstrip('/')
limits = {name: (root / name).read_text().strip() for name in
          ['cpu.max', 'memory.max', 'memory.swap.max', 'pids.max']}
quota, period = limits['cpu.max'].split()
if quota == 'max' or int(quota) != int(period):
    raise SystemExit('The capacity CPU limit must be one CPU')
if limits['memory.max'] != '1073741824' or limits['memory.swap.max'] != '0' or limits['pids.max'] != '256':
    raise SystemExit('The capacity memory or process limit is incorrect')
pathlib.Path(sys.argv[1]).write_text(json.dumps(limits, indent=2) + '\n')
PY
cd acceptance
"$STEGO_CAPACITY_BIN_DIR/acceptance.test" -test.v -test.run='^TestRealProviderAccountCapacity$' -test.count=1 -test.timeout=15m
