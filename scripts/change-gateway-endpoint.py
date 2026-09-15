#!/usr/bin/env python3
"""Apply the fixed address-test plan with the operator's explicit context.

The Pod can request only the recorded transition. An uncertain request result
keeps the same journal and is checked again. No Pod manifest is accepted.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

from gateway_endpoint_fixture import inputs, read_json, verify_policy_change
from network_peer_fixture import listener_addresses, require_owner


class ObservationError(RuntimeError):
    pass


def policy_value(value):
    result = copy.deepcopy(value)
    result.pop('status', None)
    result['metadata'] = {key: value for key, value in result['metadata'].items()
                          if key not in {'uid', 'resourceVersion', 'generation', 'creationTimestamp', 'managedFields', 'selfLink'}}
    match = result['spec']['matchConstraints']
    match.setdefault('matchPolicy', 'Equivalent')
    match.setdefault('namespaceSelector', {})
    match.setdefault('objectSelector', {})
    for rule in match['resourceRules']:
        rule.setdefault('scope', '*')
    return result


def checked_patch(current, before, after, uid):
    if (not uid or current['metadata']['uid'] != uid or not current['metadata'].get('resourceVersion') or
            current['metadata'].get('deletionTimestamp') or policy_value(current) != policy_value(before)):
        raise ValueError('The endpoint policy identity or original specification changed')
    return [{'op': 'test', 'path': '/metadata/uid', 'value': uid},
            {'op': 'test', 'path': '/metadata/resourceVersion', 'value': current['metadata']['resourceVersion']},
            {'op': 'test', 'path': '/spec', 'value': current['spec']},
            {'op': 'replace', 'path': '/spec', 'value': after['spec']}]


class Change:
    name = 'hypershell-namespace-allocation'

    def __init__(self, source, results, context, namespace, pod):
        if not context or not re.fullmatch(r'service-check-[a-z0-9]+', pod):
            raise ValueError('Require the saved context and the current test Pod')
        self.source, self.root = Path(source), Path(results)
        self.context, self.namespace, self.pod = context, namespace, pod
        self.path = self.root / 'endpoint-change/journal.json'
        self.input = inputs(self.source, self.root, namespace)
        self.installation = read_json(self.root / 'cluster-installation.json')
        self.plan = self.installation.get('endpoint_change')
        if not self.input or not self.plan or self.installation['namespace'] != namespace:
            raise ValueError('The generated endpoint installation plan is missing')
        if any(self.plan.get(key) != value for key, value in self.input.items()):
            raise ValueError('The endpoint plan differs from the listener record')
        verify_policy_change(self.plan['before'], self.plan['after'], self.input['initial'], self.input['replacement'])
        self.policy = self.plan['before']['metadata']['name']
        if self.policy != namespace + '.' + self.name + '.allocation':
            raise ValueError('The endpoint policy is outside this installation')
        matches = [item['uid'] for item in self.installation['resources']
                   if item['kind'] == 'ValidatingAdmissionPolicy' and item['name'] == self.policy]
        if len(matches) != 1 or not matches[0]:
            raise ValueError('The installed endpoint policy identity is missing')
        self.uid = matches[0]
        for name, digest in self.installation['source_sha256'].items():
            if hashlib.sha256((self.source / name).read_bytes()).hexdigest() != digest:
                raise ValueError('The frozen renderer source changed')
        self.replacement = (self.root / 'endpoint-change' / (self.name + '.json')).read_bytes()
        initial = (self.root / 'cluster-manifests' / (self.name + '.json')).read_bytes()
        if (hashlib.sha256(initial).hexdigest() != self.plan['initial_manifest_sha256'] or
                hashlib.sha256(self.replacement).hexdigest() != self.plan['replacement_manifest_sha256']):
            raise ValueError('The recorded endpoint manifests changed')

    def request(self, *words, body=None):
        try:
            value = subprocess.run(['oc', '--context=' + self.context, '--request-timeout=20s', *words],
                                   input=body, capture_output=True, timeout=30)
        except subprocess.TimeoutExpired as error:
            raise ObservationError('The endpoint request timed out; inspect the same transition again') from error
        if value.returncode:
            raise ObservationError('The endpoint request did not complete: ' + value.stderr[:2048].decode(errors='replace'))
        return value.stdout

    def object(self, *words):
        return json.loads(self.request(*words, '-o', 'json'))

    def save(self, record):
        temporary = self.path.with_suffix('.tmp')
        with temporary.open('w') as output:
            output.write(json.dumps(record, indent=2) + '\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, self.path)

    def guard(self, record):
        peer = read_json(self.root / 'network-peer.json')
        lease = self.object('-n', 'stego-ci', 'get', 'lease', 'jshell-live-test')
        annotations = lease['metadata'].get('annotations', {})
        if (lease['metadata']['uid'] != peer['lease_uid'] or lease['spec'].get('holderIdentity') != self.namespace or
                annotations.get('stego.test/namespace') != self.namespace or annotations.get('stego.test/job') != 'service-check'):
            raise ValueError('The endpoint test Lease identity changed')
        control = self.object('get', 'namespace', self.namespace)
        pod = self.object('-n', self.namespace, 'get', 'pod', self.pod)
        if (control['metadata']['uid'] != record['control_uid'] or control['metadata'].get('deletionTimestamp') or
                pod['metadata']['uid'] != record['pod_uid'] or pod['metadata'].get('deletionTimestamp') or
                pod.get('status', {}).get('phase') != 'Running'):
            raise ValueError('The endpoint test namespace or Pod identity changed')
        require_owner(self.object('get', 'namespace', self.input['namespace']), peer)
        pods = {name: self.object('-n', self.input['namespace'], 'get', 'pod', name) for name in ('address-a', 'address-b')}
        if listener_addresses(pods, peer['pods']) != peer['address_listeners']:
            raise ValueError('The endpoint listener addresses changed')

    def prepare(self):
        if self.path.exists():
            raise ValueError('The endpoint transition already has a journal')
        control = self.object('get', 'namespace', self.namespace)
        pod = self.object('-n', self.namespace, 'get', 'pod', self.pod)
        record = {'phase': 'prepared', 'control_uid': control['metadata']['uid'], 'pod_uid': pod['metadata']['uid'],
                  'policy_uid': self.uid, 'nonce': self.input['nonce']}
        self.guard(record)
        current = self.object('get', 'validatingadmissionpolicy', self.policy)
        checked_patch(current, self.plan['before'], self.plan['after'], self.uid)
        self.save(record)

    def step(self):
        record = read_json(self.path)
        if record.get('policy_uid') != self.uid or record.get('nonce') != self.input['nonce']:
            raise ValueError('The endpoint journal has another owner')
        if record['phase'] == 'complete':
            return
        if record['phase'] not in {'prepared', 'patching', 'checking', 'delivering'}:
            raise ValueError('The endpoint journal phase is invalid')
        raw = self.request('-n', self.namespace, 'exec', self.pod, '-c', 'test', '--', 'sh', '-c',
                           'if test -f /work/network-endpoint-change.request; then head -c 1025 /work/network-endpoint-change.request; fi')
        if not raw:
            return
        if len(raw) > 1024 or json.loads(raw) != {'nonce': self.input['nonce'], 'action': 'replace'}:
            raise ValueError('The Pod requested an undeclared endpoint transition')
        self.guard(record)
        current = self.object('get', 'validatingadmissionpolicy', self.policy)
        if current['metadata']['uid'] != self.uid or current['metadata'].get('deletionTimestamp'):
            raise ValueError('The endpoint policy was replaced or is being removed')
        if policy_value(current) != policy_value(self.plan['after']):
            patch = checked_patch(current, self.plan['before'], self.plan['after'], self.uid)
            if record['phase'] not in {'prepared', 'patching'}:
                raise ValueError('The checked endpoint policy was reverted')
            record['phase'] = 'patching'
            self.save(record)
            # Save before the write. If the response is lost, the next step
            # reads this exact UID and compares its complete specification.
            self.request('patch', 'validatingadmissionpolicy', self.policy, '--type=json',
                         '-p', json.dumps(patch), '-o', 'json')
            current = self.object('get', 'validatingadmissionpolicy', self.policy)
        elif record['phase'] == 'prepared':
            raise ValueError('The endpoint policy changed before the recorded write')
        if current['metadata']['uid'] != self.uid or policy_value(current) != policy_value(self.plan['after']):
            raise ValueError('The endpoint policy differs after the write')
        record['phase'] = 'checking'
        self.save(record)
        status = current.get('status', {})
        if status.get('observedGeneration') != current['metadata']['generation'] or 'typeChecking' not in status:
            return
        if status['typeChecking'].get('expressionWarnings'):
            raise ValueError('The changed admission policy has type-check warnings')
        record.update(phase='delivering', generation=current['metadata']['generation'],
                      resource_version=current['metadata']['resourceVersion'], type_checks='passed')
        self.save(record)
        target = '/work/cluster-manifests/' + self.name + '.json'
        self.request('-n', self.namespace, 'exec', '-i', self.pod, '-c', 'test', '--', 'sh', '-c',
                     'cat > ' + target + '.tmp && mv ' + target + '.tmp ' + target, body=self.replacement)
        acknowledgement = json.dumps({'nonce': self.input['nonce'], 'action': 'replace', 'policy_uid': self.uid,
                                      'generation': record['generation']}).encode()
        self.request('-n', self.namespace, 'exec', '-i', self.pod, '-c', 'test', '--', 'sh', '-c',
                     'cat > /work/network-endpoint-change.ack.tmp && mv /work/network-endpoint-change.ack.tmp /work/network-endpoint-change.ack',
                     body=acknowledgement)
        record['phase'] = 'complete'
        self.save(record)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['prepare', 'step'])
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', required=True)
    parser.add_argument('--pod', required=True)
    parser.add_argument('--results', required=True, type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    change = Change(Path(__file__).resolve().parent.parent, args.results, args.context, args.namespace, args.pod)
    try:
        getattr(change, args.action)()
    except ObservationError as error:
        print(str(error), file=sys.stderr)
        return 75
    return 0


if __name__ == '__main__':
    sys.exit(main())
