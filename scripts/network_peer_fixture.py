"""Run a bounded, unrelated listener for the direct Gateway network check."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time
import uuid

IMAGE = 'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393'
COMMAND = "require('node:net').createServer(s=>s.end()).listen(8080,'0.0.0.0')"


def peer_namespace(control):
    if not re.fullmatch(r'stego-service-[0-9]{8}-[0-9a-f]{6}', control):
        raise ValueError('Require a disposable direct test namespace')
    return control + '-peer'


def definitions(control, nonce):
    namespace = peer_namespace(control)
    labels = {'stego.test/browser-run': control, 'stego.test/peer-run': nonce}
    def item(kind, name, api='v1', **fields):
        selected = dict(labels)
        if kind == 'Pod':
            selected['app'] = 'stego-network-peer'
        return dict(apiVersion=api, kind=kind, metadata={'name': name, 'namespace': namespace, 'labels': selected}, **fields)
    return [
        {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': namespace, 'labels': {
            **labels, 'pod-security.kubernetes.io/enforce': 'restricted'}}},
        item('ResourceQuota', 'peer', spec={'hard': {'pods': '1', 'services': '1',
            'limits.cpu': '100m', 'limits.memory': '128Mi', 'limits.ephemeral-storage': '16Mi',
            'persistentvolumeclaims': '0'}}),
        # Incoming traffic is open. A failed connection must not be caused by
        # this namespace's ingress policy. The listener cannot start traffic.
        item('NetworkPolicy', 'peer', 'networking.k8s.io/v1', spec={
            'podSelector': {}, 'policyTypes': ['Egress'], 'egress': []}),
        item('ServiceAccount', 'peer', automountServiceAccountToken=False),
        item('Service', 'peer', spec={'selector': {'app': 'stego-network-peer'},
            'ports': [{'port': 8080, 'targetPort': 8080, 'protocol': 'TCP'}]}),
        item('Pod', 'peer', spec={
            'serviceAccountName': 'peer', 'automountServiceAccountToken': False, 'restartPolicy': 'Never',
            'activeDeadlineSeconds': 1800, 'terminationGracePeriodSeconds': 1,
            'securityContext': {'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}},
            'containers': [{'name': 'peer', 'image': IMAGE, 'imagePullPolicy': 'IfNotPresent',
                'command': ['node', '-e', COMMAND],
                'securityContext': {'readOnlyRootFilesystem': True, 'allowPrivilegeEscalation': False,
                                    'capabilities': {'drop': ['ALL']}},
                'resources': {'requests': {'cpu': '20m', 'memory': '32Mi', 'ephemeral-storage': '1Mi'},
                              'limits': {'cpu': '100m', 'memory': '128Mi', 'ephemeral-storage': '16Mi'}},
                'readinessProbe': {'tcpSocket': {'port': 8080}, 'periodSeconds': 1, 'timeoutSeconds': 1}}]})]


def require_owner(value, record):
    meta = value['metadata']
    labels = meta.get('labels', {})
    if (meta['name'] != record['namespace'] or
            labels.get('stego.test/browser-run') != record['control'] or
            labels.get('stego.test/peer-run') != record['nonce'] or
            record.get('namespace_uid', meta['uid']) != meta['uid']):
        raise RuntimeError('The peer namespace owner or identity changed; preserve it')


class Fixture:
    def __init__(self, context, control, directory):
        if not context or not context.strip():
            raise ValueError('Require an explicit saved cluster context')
        self.context, self.control = context, control
        self.namespace = peer_namespace(control)
        self.path = Path(directory) / 'network-peer.json'

    def request(self, *words, body=None):
        result = subprocess.run(['oc', '--context=' + self.context, '--request-timeout=20s', *words],
                                input=None if body is None else json.dumps(body), text=True,
                                capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError('Peer fixture request failed: ' + result.stderr[:4096])
        return json.loads(result.stdout) if result.stdout.strip() else None

    def save(self, record):
        temporary = self.path.with_suffix('.tmp')
        with temporary.open('w') as output:
            output.write(json.dumps(record, indent=2) + '\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, self.path)

    def lease(self):
        value = self.request('-n', 'stego-ci', 'get', 'lease', 'jshell-live-test', '-o', 'json')
        annotations = value['metadata'].get('annotations', {})
        if (value['spec'].get('holderIdentity') != self.control or
                annotations.get('stego.test/namespace') != self.control or
                annotations.get('stego.test/job') != 'service-check'):
            raise RuntimeError('The direct test does not hold the shared Lease')
        return value['metadata']['uid']

    def create(self):
        lease_uid = self.lease()
        if self.path.exists() or self.request('get', 'namespace', self.namespace, '--ignore-not-found', '-o', 'json'):
            raise RuntimeError('The peer fixture already exists; do not adopt it')
        record = {'control': self.control, 'namespace': self.namespace, 'nonce': uuid.uuid4().hex,
                  'phase': 'creating', 'host': 'peer.' + self.namespace + '.svc.cluster.local', 'port': 8080,
                  'lease_uid': lease_uid}
        self.save(record)
        for item in definitions(self.control, record['nonce']):
            created = self.request('create', '-f', '-', '-o', 'json', body=item)
            if item['kind'] == 'Namespace':
                record['namespace_uid'] = created['metadata']['uid']
            record['last_created'] = {'kind': item['kind'], 'uid': created['metadata']['uid']}
            self.save(record)
            if item['kind'] == 'Namespace':
                deadline = time.monotonic() + 30
                while True:
                    current = self.request('get', 'namespace', self.namespace, '-o', 'json')
                    require_owner(current, record)
                    annotations = current['metadata'].get('annotations', {})
                    ranges = [annotations.get('openshift.io/sa.scc.' + name, '').split('/')[0]
                              for name in ['uid-range', 'supplemental-groups']]
                    if all(value.isdigit() and int(value) > 0 for value in ranges):
                        break
                    if time.monotonic() >= deadline:
                        raise RuntimeError('OpenShift did not assign the peer namespace security ranges')
                    time.sleep(0.5)
        deadline = time.monotonic() + 150
        while True:
            pod = self.request('-n', self.namespace, 'get', 'pod', 'peer', '-o', 'json')
            if any(c['type'] == 'Ready' and c['status'] == 'True' for c in pod.get('status', {}).get('conditions', [])):
                break
            if pod.get('status', {}).get('phase') in {'Failed', 'Succeeded'} or time.monotonic() >= deadline:
                raise RuntimeError('The peer listener did not become ready')
            time.sleep(1)
        require_owner(self.request('get', 'namespace', self.namespace, '-o', 'json'), record)
        if pod['metadata']['uid'] != record['last_created']['uid']:
            raise RuntimeError('The peer listener was replaced')
        policies = self.request('-n', self.namespace, 'get', 'networkpolicies', '-o', 'json')['items']
        if len(policies) != 1:
            raise RuntimeError('The peer namespace has an unexpected policy set')
        policy = policies[0]
        spec = policy['spec']
        if (policy['metadata']['name'] != 'peer' or spec.get('podSelector') != {} or
                spec.get('policyTypes') != ['Egress'] or spec.get('egress') or spec.get('ingress')):
            raise RuntimeError('The peer namespace policy changed')
        record.update(phase='ready', pod_uid=pod['metadata']['uid'], policy_uid=policy['metadata']['uid'],
                      ingress_policy=False, outbound_connections=False)
        self.save(record)

    def cleanup(self):
        if not self.path.exists():
            return
        record = json.loads(self.path.read_text())
        if self.lease() != record['lease_uid']:
            raise RuntimeError('The shared Lease identity changed; preserve the peer')
        if record['namespace'] != self.namespace or record['control'] != self.control:
            raise RuntimeError('The peer cleanup record has another target')
        current = self.request('get', 'namespace', self.namespace, '--ignore-not-found', '-o', 'json')
        if current:
            require_owner(current, record)
            meta = current['metadata']
            record.update(phase='deleting', namespace_uid=meta['uid'])
            self.save(record)
            self.request('delete', '--raw=/api/v1/namespaces/' + self.namespace, '-f', '-', body={
                'apiVersion': 'v1', 'kind': 'DeleteOptions', 'preconditions': {
                    'uid': meta['uid'], 'resourceVersion': meta['resourceVersion']}})
        deadline = time.monotonic() + 90
        while self.request('get', 'namespace', self.namespace, '--ignore-not-found', '-o', 'json'):
            if time.monotonic() >= deadline:
                raise RuntimeError('Peer namespace deletion is not confirmed')
            time.sleep(1)
        record.update(phase='removed', namespace_absent=True)
        self.save(record)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['create', 'cleanup'])
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', required=True)
    parser.add_argument('--results', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    getattr(Fixture(args.context, args.namespace, args.results), args.action)()
