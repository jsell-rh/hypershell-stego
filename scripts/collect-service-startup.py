#!/usr/bin/env python3
"""Save bounded Pod startup evidence without container commands or credentials."""
import argparse
from datetime import datetime, timezone
import json
import os
import re
from pathlib import Path
import subprocess


def summary(pod):
    metadata, spec, status = (pod.get(k, {}) for k in ('metadata', 'spec', 'status'))
    result = {k: metadata.get(k) for k in ('name', 'namespace', 'uid', 'creationTimestamp')}
    result.update(phase=status.get('phase'), node=spec.get('nodeName'))
    result['conditions'] = [{k: c.get(k) for k in ('type', 'status', 'reason', 'message', 'lastTransitionTime')}
                            for c in status.get('conditions', [])]
    result['containers'] = [{'name': c.get('name'), 'restart_policy': c.get('restartPolicy'),
                             'resources': c.get('resources', {})}
                            for c in spec.get('containers', []) + spec.get('initContainers', [])]
    result['container_states'] = []
    for c in status.get('containerStatuses', []) + status.get('initContainerStatuses', []):
        state = c.get('state', {})
        # Termination messages can contain application output. Keep only codes.
        result['container_states'].append({'name': c.get('name'), 'ready': c.get('ready'),
            'restart_count': c.get('restartCount'), 'state': {kind: {k: value.get(k)
                for k in ('reason', 'exitCode', 'signal', 'startedAt', 'finishedAt') if k in value}
                for kind, value in state.items() if kind in ('waiting', 'running', 'terminated')}})
    return result


def collect(context, namespace):
    result = {'observed_at': datetime.now(timezone.utc).isoformat(), 'namespace': namespace,
              'job': 'service-check', 'complete': False}
    try:
        # Read only the existing test Pod. Do not execute a command in it.
        if not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', namespace):
            raise ValueError('Invalid test namespace')
        path = '/api/v1/namespaces/' + namespace + '/pods?labelSelector=job-name%3Dservice-check&limit=2'
        # A raw request reads one page. Normal oc list output follows all pages.
        response = subprocess.run(['oc', '--context=' + context, '--request-timeout=15s',
            'get', '--raw=' + path], capture_output=True, check=True, timeout=20)
        if len(response.stdout) > 2 * 1024 * 1024:
            raise ValueError('Pod response exceeds the evidence limit')
        page = json.loads(response.stdout)
        if page.get('metadata', {}).get('continue') or not isinstance(page.get('items'), list) or len(page['items']) > 2:
            raise ValueError('Pod snapshot is incomplete or exceeds the evidence limit')
        result['pods'] = [summary(p) for p in page['items']]
        result['complete'] = True
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, AttributeError) as error:
        # Do not copy subprocess output or exception text into the public record.
        result['error_type'] = type(error).__name__
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', required=True)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    result = collect(args.context, args.namespace)
    with args.output.open('x') as output:
        os.fchmod(output.fileno(), 0o600)
        json.dump(result, output, indent=2)
        output.write('\n')
    if not result['complete']:
        raise SystemExit('Startup evidence is incomplete; inspect the saved error type')


if __name__ == '__main__':
    main()
