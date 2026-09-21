#!/usr/bin/env python3
"""Read Pod termination and namespace finalizer fields for one test allocator.

This record is an observation, not a cleanup pass. It does not change resources.
Call it during the existing bounded browser workflow. Use a new output path for
each sample. Never use missing or failed samples as proof of resource removal.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
from urllib.parse import urlencode


ALLOCATOR_LABEL = 'stego.dev/allocator'
MAX_ITEMS = 16
MAX_RESPONSE = 2 * 1024 * 1024


def fields(value, names):
    result = {k: value[k] for k in names if k in value}
    if any(not isinstance(v, (str, int, bool, type(None))) or
           isinstance(v, str) and len(v) > 256 for v in result.values()):
        raise ValueError('Invalid observation field')
    return result


def array(value):
    if not isinstance(value, list) or len(value) > MAX_ITEMS:
        raise ValueError('Observation array exceeds its limit')
    return value


def identity(value):
    result = fields(value['metadata'], ('name', 'namespace', 'uid', 'creationTimestamp',
                                       'deletionTimestamp', 'deletionGracePeriodSeconds'))
    if any(not isinstance(result.get(k), str) or not result[k] for k in ('name', 'uid')):
        raise ValueError('Resource identity is absent')
    return result


def finalizers(value):
    result = array(value.get('finalizers', []))
    if any(not isinstance(v, str) or len(v) > 256 for v in result):
        raise ValueError('Invalid finalizer')
    return result


def conditions(status):
    # Messages can contain application data. Retain condition codes only.
    return [fields(v, ('type', 'status', 'reason', 'lastTransitionTime'))
            for v in array(status.get('conditions', []))]


def pod_summary(pod, namespace):
    if pod.get('kind') != 'Pod' or pod['metadata'].get('namespace') != namespace:
        raise ValueError('Pod scope differs')
    spec, status = pod.get('spec', {}), pod.get('status', {})
    result = identity(pod)
    result.update(fields(spec, ('terminationGracePeriodSeconds',)))
    result.update(fields(status, ('phase', 'reason')))
    result['finalizers'] = finalizers(pod['metadata'])
    result['owners'] = [fields(v, ('apiVersion', 'kind', 'name', 'uid', 'controller'))
                        for v in array(pod['metadata'].get('ownerReferences', []))]
    result['conditions'] = conditions(status)
    result['containers'] = []
    for key in ('initContainerStatuses', 'containerStatuses', 'ephemeralContainerStatuses'):
        for container in array(status.get(key, [])):
            row = fields(container, ('name', 'ready', 'restartCount'))
            row['status_group'] = key
            for state_key in ('state', 'lastState'):
                row[state_key] = {kind: fields(value, ('reason', 'exitCode', 'signal', 'startedAt', 'finishedAt'))
                                  for kind, value in container.get(state_key, {}).items()
                                  if kind in ('waiting', 'running', 'terminated')}
            result['containers'].append(row)
    return result


def collect(context, fixture_namespace):
    result = {'request_started_at': datetime.now(timezone.utc).isoformat(),
              'fixture_namespace': fixture_namespace, 'snapshot_complete': False,
              'cleanup_proved': False, 'cluster_changes': False}
    requests = 0
    deadline = time.monotonic() + 30
    try:
        if not context or not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', fixture_namespace):
            raise ValueError('Explicit context and fixture namespace are required')
        marker = hashlib.sha256((fixture_namespace + '.hypershell-namespace-allocation').encode()).hexdigest()[:32]

        def read(path):
            nonlocal requests
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('Observation deadline expired')
            requests += 1
            response = subprocess.run(['oc', '--context=' + context, '--request-timeout=5s',
                                       'get', '--raw=' + path], capture_output=True, check=True, timeout=min(7, remaining))
            if len(response.stdout) > MAX_RESPONSE:
                raise ValueError('Response exceeds the observation limit')
            value = json.loads(response.stdout)
            if value.get('metadata', {}).get('continue') or not isinstance(value.get('items'), list):
                raise ValueError('Incomplete observation page')
            return array(value['items'])

        path = '/api/v1/namespaces?' + urlencode({'labelSelector': ALLOCATOR_LABEL + '=' + marker,
                                                 'limit': MAX_ITEMS + 1})

        def namespaces():
            found = {}
            for item in read(path):
                meta = item['metadata']
                if item.get('kind') != 'Namespace' or meta.get('labels', {}).get(ALLOCATOR_LABEL) != marker:
                    raise ValueError('Namespace owner differs')
                name = meta['name']
                if name in found or not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', name):
                    raise ValueError('Invalid namespace name')
                identity(item)
                found[name] = item
            return found

        before = namespaces()
        selected = []
        for name, namespace in sorted(before.items()):
            row = identity(namespace)
            row['labels'] = fields(namespace['metadata'].get('labels', {}),
                                   (ALLOCATOR_LABEL, 'stego.dev/allocation-profile', 'hypershell.redhat.io/gateway-id'))
            row['metadata_finalizers'] = finalizers(namespace['metadata'])
            row['namespace_finalizers'] = finalizers(namespace.get('spec', {}))
            row['conditions'] = conditions(namespace.get('status', {}))
            row.update(fields(namespace.get('status', {}), ('phase',)))
            row['pods_read_started_at'] = datetime.now(timezone.utc).isoformat()
            pods = read('/api/v1/namespaces/' + name + '/pods?limit=' + str(MAX_ITEMS + 1))
            row['pods_read_finished_at'] = datetime.now(timezone.utc).isoformat()
            row['pods'] = sorted((pod_summary(v, name) for v in pods), key=lambda v: (v['name'], v['uid']))
            if len({v['uid'] for v in row['pods']}) != len(row['pods']):
                raise ValueError('Duplicate Pod identity')
            selected.append(row)
        after = namespaces()
        if {k: v['metadata']['uid'] for k, v in before.items()} != {k: v['metadata']['uid'] for k, v in after.items()}:
            raise ValueError('Namespace selection changed during the read')
        result.update(snapshot_complete=True, namespaces=selected, allocator=marker)
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, AttributeError, KeyError) as error:
        # Keep no partial snapshot, exception text, command output, or Pod spec.
        result['error_type'] = type(error).__name__
    result.update(read_finished_at=datetime.now(timezone.utc).isoformat(), requests=requests)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--fixture-namespace', required=True)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    result = collect(args.context, args.fixture_namespace)
    with args.output.open('x') as output:
        os.fchmod(output.fileno(), 0o600)
        json.dump(result, output, indent=2)
        output.write('\n')
    if not result['snapshot_complete']:
        raise SystemExit('Cleanup observation is incomplete; inspect the saved error type')


if __name__ == '__main__':
    main()
