#!/usr/bin/env python3
"""Require a complete bounded real-provider account cleanup result."""
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
record = json.loads((root / 'result.json').read_text())
required = {
    'schema': 1, 'scope': 'account_cleanup', 'stage': 'complete',
    'gateways': 100, 'accounts_per_gateway': 100,
    'seeded_background_accounts': 9900, 'rest_created_accounts': 100,
    'target_seconds': 30, 'target_met': True, 'complete': True,
    'keycloak_mode': 'start', 'keycloak_database': 'postgresql',
    'workload_cleanup_tested': False, 'background_creation_tested': False,
    'race_instrumented': False, 'test_passed': True,
    'sql_accounts_before': 10000, 'provider_account_clients_before': 10000,
    'closed_accounts': 100, 'closed_journals': 100,
    'deleted_provider_clients': 100, 'deleted_provider_users': 100,
    'preserved_background_accounts': 9900,
}
for key, value in required.items():
    if type(record.get(key)) is not type(value) or record.get(key) != value:
        raise SystemExit('Missing or invalid capacity result: ' + key)
if not 0 < record['scope_sealed_seconds'] <= record['elapsed_seconds'] <= 30:
    raise SystemExit('The measured cleanup missed its target')
for key in ['background_sql_sha256', 'provider_snapshot_sha256']:
    if not re.fullmatch('[0-9a-f]{64}', record.get(key, '')):
        raise SystemExit('The preservation digest is absent: ' + key)
if record.get('preserved_other_clients', 0) < 10000:
    raise SystemExit('The provider preservation count is too small')
limits = json.loads((root / 'cgroup-limits.json').read_text())
quota, period = limits['cpu.max'].split()
if quota == 'max' or int(quota) != int(period):
    raise SystemExit('The CPU limit is invalid')
if limits['memory.max'] != '1073741824' or limits['memory.swap.max'] != '0' or limits['pids.max'] != '256':
    raise SystemExit('The memory or process limit is invalid')
if (root / 'process-exit-code').read_text().strip() != '0':
    raise SystemExit('The test process did not exit with success')
if not re.search(r'^--- PASS: TestRealProviderAccountCapacity ', (root / 'tests.log').read_text(), re.M):
    raise SystemExit('The required test pass is absent')
(root / 'verification.json').write_text(json.dumps({'verified': True, 'scope': 'account_cleanup'}, indent=2) + '\n')
print('The bounded real-provider account cleanup result is complete.')
