#!/usr/bin/env python3
"""Check recorded CNPG volumes after the browser workflow. Do not delete them."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tarfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--evidence', required=True, type=Path)
    args = parser.parse_args()
    plan = json.loads((args.evidence / 'cnpg-plan.json').read_text())
    with tarfile.open(args.evidence / 'evidence.tar') as archive:
        member = archive.getmember('browser-artifacts/database-deletion-storage.json')
        if not member.isfile() or member.size > 65536:
            raise RuntimeError('Invalid database storage evidence')
        record = json.load(archive.extractfile(member))
    if record['Namespace'] != plan['database_namespace']:
        raise RuntimeError('Database storage evidence has a different namespace')
    volumes = record['Volumes']
    if not isinstance(volumes, list) or not 1 <= len(volumes) <= 2:
        raise RuntimeError('Unexpected database volume count')
    names = set()
    for volume in volumes:
        for field in ('Claim', 'Volume'):
            name = volume[field]
            if not isinstance(name, str) or len(name) > 253 or not re.fullmatch(r'[a-z0-9][a-z0-9.-]*[a-z0-9]', name):
                raise RuntimeError('Invalid database storage name')
        if not isinstance(volume['UID'], str) or not re.fullmatch(r'[a-f0-9-]{36}', volume['UID']):
            raise RuntimeError('Invalid database claim UID')
        names.add(volume['Volume'])
    if len(names) != len(volumes):
        raise RuntimeError('Database volume evidence contains duplicates')
    deadline = time.monotonic() + 120
    pending = list(volumes)
    while pending:
        remaining = []
        for volume in pending:
            result = subprocess.run(
                ['oc', '--context=' + args.context, '--request-timeout=10s',
                 'get', 'persistentvolume', volume['Volume'], '--ignore-not-found', '-o', 'json'],
                capture_output=True, text=True, timeout=15, check=True)
            if result.stdout.strip():
                stored = json.loads(result.stdout)
                claim = stored.get('spec', {}).get('claimRef', {})
                if (claim.get('namespace'), claim.get('name'), claim.get('uid')) != (
                        record['Namespace'], volume['Claim'], volume['UID']):
                    raise RuntimeError('Recorded volume no longer has the expected claim')
                remaining.append(volume)
        pending = remaining
        if pending:
            if time.monotonic() >= deadline:
                raise RuntimeError('Database volumes remain after controller cleanup')
            time.sleep(2)
    report = dict(namespace=record['Namespace'], volumes_absent=sorted(names))
    (args.evidence / 'database-volume-deletion.json').write_text(json.dumps(report, indent=2) + '\n')
    print('Verified database volume removal:', len(names))


if __name__ == '__main__':
    main()
