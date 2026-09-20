#!/usr/bin/env python3
"""Select an exact successful image run before the live test gets credentials."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def document(text):
    if len(text.encode()) > 65536:
        raise ValueError('The input policy exceeds its size limit')

    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('The input policy repeats a field')
            result[key] = value
        return result

    result = json.loads(text, object_pairs_hook=unique)
    if not isinstance(result, dict):
        raise ValueError('The input policy must be an object')
    return result


def validate_run(run, repository, revision, number, attempt, policy):
    expected = {
        'id': int(number), 'head_sha': revision, 'run_attempt': int(attempt),
        'status': 'completed', 'conclusion': 'success',
        'path': '.github/workflows/application-images.yml',
    }
    if any(run.get(key) != value for key, value in expected.items()):
        raise ValueError('The selected image run differs or did not pass')
    if run.get('repository', {}).get('full_name') != repository:
        raise ValueError('The selected image run uses a different repository')
    if run.get('event') not in {'push', 'workflow_dispatch'}:
        raise ValueError('The selected image run uses a different event')
    expected_policy = {
        'format': 2, 'repository': repository, 'revision': revision,
        'application_revision': revision,
        'reference': 'refs/heads/' + run['head_branch'],
    }
    if any(policy.get(key) != value for key, value in expected_policy.items()):
        raise ValueError('The independent policy does not select this image run')
    return expected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--recheck', action='store_true')
    args = parser.parse_args()
    root = Path(os.environ['RUNNER_TEMP'])
    repository = os.environ['GITHUB_REPOSITORY']
    revision = os.environ['FIXTURE_REVISION']
    number, attempt = os.environ['IMAGE_RUN'], os.environ['IMAGE_ATTEMPT']
    if not re.fullmatch('[0-9a-f]{40}', revision):
        raise ValueError('Select a full fixture commit ID')
    if not all(re.fullmatch('[1-9][0-9]{0,14}', value) for value in (number, attempt)):
        raise ValueError('Select a positive image run and attempt')
    if not re.fullmatch('[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('The workflow repository is invalid')
    policy_path = root / 'image-policy.json'
    registry_path = root / 'registry-policy.json'
    record_path = root / 'image-run-selection.json'
    if args.recheck:
        policy = document(policy_path.read_text())
        record = document(record_path.read_text())
        if hashlib.sha256(policy_path.read_bytes()).hexdigest() != record['policy_sha256']:
            raise ValueError('The selected policy changed during download')
    else:
        policy = document(os.environ['IMAGE_POLICY'])
        registry = document(os.environ['REGISTRY_POLICY'])
    raw = subprocess.check_output([
        'gh', 'api', '--hostname', 'github.com',
        f'repos/{repository}/actions/runs/{number}',
    ], timeout=45)
    run = json.loads(raw)
    selected = validate_run(run, repository, revision, number, attempt, policy)
    if args.recheck:
        if record['run'] != selected:
            raise ValueError('The selected run changed during download')
        return
    # The common STEGO stage and publisher validate the full closed policies.
    # These records retain the independent operator inputs before cluster use.
    for path, value in [(policy_path, policy), (registry_path, registry)]:
        with path.open('x') as stream:
            stream.write(json.dumps(value, indent=2) + '\n')
    with record_path.open('x') as stream:
        json.dump({'repository': repository, 'run': selected,
                   'policy_sha256': hashlib.sha256(policy_path.read_bytes()).hexdigest(),
                   'cluster_access_started': False}, stream, indent=2)
        stream.write('\n')
    environment = {
        'STEGO_TEST_IMAGE_ARTIFACTS': str(root / 'image-artifacts'),
        'STEGO_TEST_IMAGE_POLICY': str(policy_path),
        'STEGO_TEST_IMAGE_RUN': number,
        'STEGO_TEST_IMAGE_ATTEMPT': attempt,
        'STEGO_TEST_REGISTRY_POLICY': str(registry_path),
    }
    with Path(os.environ['GITHUB_ENV']).open('a') as stream:
        for key, value in environment.items():
            if '\n' in value or '\r' in value:
                raise ValueError('The workflow path contains a line break')
            stream.write(key + '=' + value + '\n')
    print('Selected the exact image run. Common signature checks must still pass.')


if __name__ == '__main__':
    main()
