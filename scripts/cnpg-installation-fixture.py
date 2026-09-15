#!/usr/bin/env python3
"""Create and remove the bounded CNPG server for the complete Gateway test.

Run with the operator context while holding the shared live-test Lease. The
application test uses the restricted CI identity. Journals contain no secrets.
"""
import argparse
import base64
import hashlib
import ipaddress
import json
from pathlib import Path
import re
import secrets
import subprocess
import time

LABEL = 'stego.test/cnpg-run'
CLUSTER = 'gateway-database'
IMAGE = 'ghcr.io/cloudnative-pg/postgresql:18.6-system-trixie@sha256:14e57107afc9bcdd085ed6593c67743723718f167da512326ddb2f7904cc4576'
DEADLINE_IMAGE = 'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393'


def validate(namespace, database_namespace, storage_class):
    if namespace != 'stego-service-ci':
        raise ValueError('Use the fixed restricted browser test namespace')
    if not re.fullmatch(r'stego-cnpg-database-[a-z0-9](?:[a-z0-9-]*[a-z0-9])?', database_namespace) or len(database_namespace) > 63:
        raise ValueError('Use a dedicated CNPG installation namespace')
    if not re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', storage_class) or len(storage_class) > 253:
        raise ValueError('Use an explicit storage class')


def definitions(namespace, database_namespace, storage_class, endpoints):
    validate(namespace, database_namespace, storage_class)
    if not 1 <= len(endpoints) <= 16:
        raise ValueError('Require bounded Kubernetes endpoint addresses')
    for entry in endpoints:
        if not isinstance(entry, (tuple, list)) or len(entry) != 2 or not isinstance(entry[0], str) or type(entry[1]) is not int:
            raise ValueError('Invalid Kubernetes endpoint shape')
    api_rules = []
    for host, port in sorted(set(tuple(value) for value in endpoints)):
        address = ipaddress.ip_address(host)
        if address.is_unspecified or address.is_multicast or address.is_loopback or not isinstance(port, int) or not 1 <= port <= 65535:
            raise ValueError('Invalid Kubernetes endpoint address')
        api_rules.append({'to': [{'ipBlock': {'cidr': str(address) + ('/32' if address.version == 4 else '/128')}}], 'ports': [{'port': port, 'protocol': 'TCP'}]})
    def item(kind, name, api='v1', **fields):
        return dict(apiVersion=api, kind=kind, metadata={'name': name, 'namespace': database_namespace, 'labels': {LABEL: namespace}}, **fields)
    def binding(name, role, subject, subject_namespace, kind='ClusterRole'):
        return item('RoleBinding', name, 'rbac.authorization.k8s.io/v1',
                    roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': kind, 'name': role},
                    subjects=[{'kind': 'ServiceAccount', 'name': subject, 'namespace': subject_namespace}])
    def peer(ns, labels):
        return {'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': ns}}, 'podSelector': {'matchLabels': labels}}
    sql = [{'port': 5432, 'protocol': 'TCP'}]
    cluster_peer = peer(database_namespace, {'cnpg.io/cluster': CLUSTER})
    marker = hashlib.sha256((namespace + '.hypershell-namespace-allocation').encode()).hexdigest()[:32]
    gateway_peer = {'namespaceSelector': {'matchLabels': {'stego.dev/allocator': marker, 'stego.dev/allocation-profile': 'gateway'}},
                    'podSelector': {'matchExpressions': [{'key': 'hypershell.redhat.io/gateway-id', 'operator': 'Exists'}]}}
    network = item('NetworkPolicy', 'database', 'networking.k8s.io/v1', spec={
        'podSelector': {'matchLabels': {'cnpg.io/cluster': CLUSTER}}, 'policyTypes': ['Ingress', 'Egress'],
        'ingress': [
            {'from': [cluster_peer], 'ports': sql + [{'port': 8000, 'protocol': 'TCP'}]},
            {'from': [peer('cnpg-system', {'app.kubernetes.io/name': 'cloudnative-pg'})], 'ports': [{'port': 8000, 'protocol': 'TCP'}]},
            {'from': [peer(namespace, {'app': 'stego-fixture'}), peer(namespace, {'app.kubernetes.io/name': 'hypershell-gateway-workload'}), gateway_peer], 'ports': sql}],
        'egress': [
            {'to': [cluster_peer], 'ports': sql + [{'port': 8000, 'protocol': 'TCP'}]},
            {'to': [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'openshift-dns'}}}],
             'ports': [{'port': 5353, 'protocol': 'UDP'}, {'port': 5353, 'protocol': 'TCP'}]}, *api_rules]})
    return [
        {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': database_namespace, 'labels': {LABEL: namespace, 'pod-security.kubernetes.io/enforce': 'restricted'}}},
        item('ResourceQuota', 'installation', spec={'hard': {'pods': '6', 'limits.cpu': '3100m', 'limits.memory': '3200Mi', 'limits.ephemeral-storage': '2Gi', 'persistentvolumeclaims': '4', 'requests.storage': '4Gi'}}),
        binding('cnpg-manager', 'cnpg-manager', 'cnpg-manager', 'cnpg-system'),
        binding('database-nonroot', 'system:openshift:scc:nonroot-v2', CLUSTER, database_namespace),
        item('Role', 'fixture-observer', 'rbac.authorization.k8s.io/v1', rules=[
            {'apiGroups': ['postgresql.cnpg.io'], 'resources': ['clusters'], 'resourceNames': [CLUSTER], 'verbs': ['get']},
            {'apiGroups': [''], 'resources': ['services'], 'resourceNames': [CLUSTER + '-rw'], 'verbs': ['get']},
            {'apiGroups': [''], 'resources': ['pods'], 'verbs': ['get', 'delete']}]),
        binding('fixture-observer', 'fixture-observer', 'service-check', namespace, 'Role'),
        network,
        item('Job', 'database-lifetime', 'batch/v1', spec={'backoffLimit': 0, 'activeDeadlineSeconds': 1500, 'ttlSecondsAfterFinished': 0,
            'template': {'spec': {'restartPolicy': 'Never', 'automountServiceAccountToken': False,
                'securityContext': {'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}},
                'containers': [{'name': 'deadline', 'image': DEADLINE_IMAGE, 'command': ['/bin/sleep', '3600'],
                    'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                    'resources': {'requests': {'cpu': '5m', 'memory': '8Mi', 'ephemeral-storage': '1Mi'}, 'limits': {'cpu': '50m', 'memory': '32Mi', 'ephemeral-storage': '16Mi'}}}]}}}),
        item('Cluster', CLUSTER, 'postgresql.cnpg.io/v1', spec={
            'instances': 2, 'imageName': IMAGE, 'enableSuperuserAccess': True, 'superuserSecret': {'name': 'fixture-postgres'},
            'bootstrap': {'initdb': {'database': 'installation', 'owner': 'installation', 'dataChecksums': True}},
            'storage': {'size': '1Gi', 'storageClass': storage_class},
            'resources': {'requests': {'cpu': '100m', 'memory': '256Mi', 'ephemeral-storage': '32Mi'}, 'limits': {'cpu': '500m', 'memory': '768Mi', 'ephemeral-storage': '256Mi'}},
            'postgresql': {'parameters': {'max_connections': '100', 'shared_buffers': '128MB', 'work_mem': '4MB', 'maintenance_work_mem': '32MB'},
                           'pg_hba': ['hostnossl all all all reject', 'hostssl all all all scram-sha-256']}}),
    ]


def write(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


class Installation:
    def __init__(self, args):
        self.args = args
        self.journal = args.results / 'cnpg-server-created.json'

    def oc(self, *words, data=None):
        # Secret contents and raw server errors must not enter public evidence.
        read = bool(words) and words[0] == 'get'
        attempts = 3 if read else 1
        for attempt in range(attempts):
            try:
                result = subprocess.run(['oc', '--context=' + self.args.context, '--request-timeout=20s', *words],
                                        input=None if data is None else json.dumps(data), capture_output=True, text=True, timeout=30)
                if result.returncode:
                    raise RuntimeError('request failed')
                if not result.stdout.strip():
                    if read and '--ignore-not-found' not in words:
                        raise ValueError('read has no JSON response')
                    return None
                value = json.loads(result.stdout)
                if read:
                    if not isinstance(value, dict):
                        raise ValueError('read is not an object')
                    if 'items' in value:
                        if not isinstance(value['items'], list):
                            raise ValueError('read has no item list')
                    elif not isinstance(value.get('metadata'), dict) or not value['metadata'].get('uid'):
                        raise ValueError('read has no resource identity')
                return value
            except (OSError, subprocess.SubprocessError, ValueError, RuntimeError):
                if attempt + 1 == attempts:
                    budget = 'three attempts' if read else 'one attempt'
                    operation = words[0] if words else 'empty command'
                    raise RuntimeError('CNPG installation request failed after ' + budget + ': ' + operation) from None
            time.sleep(attempt + 1)

    def get(self, kind, name, namespace=None):
        words = ['get', kind, name, '--ignore-not-found', '-o', 'json']
        if namespace:
            words += ['-n', namespace]
        return self.oc(*words)

    def require_lease(self):
        if not re.fullmatch(r'stego-cnpg-live-[0-9a-f]{16}', self.args.lease_holder) or not self.args.lease_uid:
            raise RuntimeError('Require a distinct CNPG run holder and Lease UID')
        lease = self.get('lease', 'jshell-live-test', 'stego-ci')
        if lease['metadata']['uid'] != self.args.lease_uid or lease.get('spec', {}).get('holderIdentity') != self.args.lease_holder:
            raise RuntimeError('The CNPG installation requires its held live-test Lease')
        if lease['metadata'].get('annotations', {}).get('stego.test/namespace') != self.args.namespace or lease['metadata']['annotations'].get('stego.test/job') != 'service-check':
            raise RuntimeError('The CNPG Lease does not name the browser test')

    def create(self, item, journal):
        item['metadata'].setdefault('annotations', {})['stego.test/cnpg-holder'] = self.args.lease_holder
        record = {'apiVersion': item['apiVersion'], 'kind': item['kind'],
                  'metadata': {k: item['metadata'][k] for k in ['name', 'namespace'] if k in item['metadata']}, 'state': 'requested'}
        journal.append(record)
        write(self.journal, journal)
        try:
            created = self.oc('create', '-f', '-', '-o', 'json', data=item)
        except (RuntimeError, subprocess.TimeoutExpired):
            # A lost response does not prove that creation failed. Record a
            # matching observed object for cleanup, then stop this attempt.
            kind = 'clusters.postgresql.cnpg.io' if item['kind'] == 'Cluster' else item['kind']
            observed = self.get(kind, item['metadata']['name'], item['metadata'].get('namespace'))
            if observed and observed['metadata'].get('annotations', {}).get('stego.test/cnpg-holder') == self.args.lease_holder and observed['metadata'].get('labels', {}).get(LABEL) == self.args.namespace:
                record['metadata']['uid'] = observed['metadata']['uid']
                record['state'] = 'observed_after_error'
                write(self.journal, journal)
            raise
        record['metadata']['uid'] = created['metadata']['uid']
        record['state'] = 'created'
        write(self.journal, journal)
        return created

    def install(self):
        self.require_lease()
        if self.journal.exists():
            raise RuntimeError('Inspect the existing CNPG server journal before another installation')
        config = self.get('configmap', 'browser-ci-installation', self.args.namespace)
        data = config['data']
        endpoints = []
        slices = json.loads(data['kubernetes-endpoints.json'])
        for item in slices['items']:
            for endpoint in item['endpoints']:
                if endpoint.get('conditions', {}).get('ready') is True:
                    endpoints += [(address, port['port']) for address in endpoint['addresses'] for port in item['ports'] if port.get('name') == 'https' and port.get('protocol') == 'TCP']
        service = json.loads(data['kubernetes-service.json'])
        endpoints += [(address, 443) for address in service['spec'].get('clusterIPs', [service['spec']['clusterIP']])]
        storage = self.get('storageclass', self.args.storage_class)
        if not storage or storage.get('reclaimPolicy') != 'Delete' or storage.get('provisioner') == 'kubernetes.io/no-provisioner':
            raise RuntimeError('The CNPG fixture requires dynamic storage with Delete reclaim policy')
        items = definitions(self.args.namespace, self.args.database_namespace, self.args.storage_class, endpoints)
        # Check all target identities before the first write. This namespace is
        # exclusively for this installation and never a Gateway allocation.
        for kind, name, namespace in [('Namespace', self.args.database_namespace, None), ('Secret', 'cnpg-credentials', self.args.namespace)]:
            if self.get(kind, name, namespace):
                raise RuntimeError('CNPG installation refuses an existing target')
        journal = []
        write(self.journal, journal)
        for item in items:
            if item['kind'] == 'Cluster':
                break
            self.create(item, journal)
        lifetime = next(o for o in journal if o['kind'] == 'Job')
        owner = {'apiVersion': 'batch/v1', 'kind': 'Job', 'name': lifetime['metadata']['name'], 'uid': lifetime['metadata']['uid']}
        password = secrets.token_hex(32)
        secret = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'fixture-postgres', 'namespace': self.args.database_namespace, 'labels': {LABEL: self.args.namespace}, 'ownerReferences': [owner]},
                  'type': 'kubernetes.io/basic-auth', 'stringData': {'username': 'postgres', 'password': password}}
        self.create(secret, journal)
        # The scoped operator is installed with zero replicas. Start its bounded
        # lifetime before the Deployment. Keep it alive for server finalizers.
        self.oc('patch', 'job', 'cnpg-test-lifetime', '-n', 'cnpg-system', '--type=merge', '-p', '{"spec":{"suspend":false}}', '-o', 'json')
        self.oc('scale', 'deployment', 'cnpg-controller-manager', '-n', 'cnpg-system', '--replicas=1', '-o', 'json')
        deadline = time.monotonic() + 240
        while True:
            deployment = self.get('deployment', 'cnpg-controller-manager', 'cnpg-system')
            if deployment.get('status', {}).get('availableReplicas') == 1:
                break
            if time.monotonic() > deadline:
                raise RuntimeError('CNPG operator readiness was not confirmed')
            time.sleep(2)
        cluster = items[-1]
        cluster['metadata']['ownerReferences'] = [owner]
        created = self.create(cluster, journal)
        deadline = time.monotonic() + 360
        while True:
            current = self.get('clusters.postgresql.cnpg.io', CLUSTER, self.args.database_namespace)
            if current and current['metadata']['uid'] == created['metadata']['uid'] and current.get('status', {}).get('readyInstances') == 2:
                break
            if time.monotonic() > deadline:
                raise RuntimeError('CNPG server readiness was not confirmed')
            time.sleep(2)
        ca_name = current['status']['certificates']['serverCASecret']
        if not isinstance(ca_name, str) or not ca_name.startswith(CLUSTER + '-'):
            raise RuntimeError('CNPG server CA reference differs')
        ca_secret = self.get('secret', ca_name, self.args.database_namespace)
        ca = base64.b64decode(ca_secret['data']['ca.crt'], validate=True).decode()
        namespace = next(o for o in journal if o['kind'] == 'Namespace')
        fixture = {'namespace': self.args.database_namespace, 'namespace_uid': namespace['metadata']['uid'], 'cluster': CLUSTER,
                   'cluster_uid': created['metadata']['uid'], 'password': password, 'ca': ca}
        self.create({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'cnpg-credentials', 'namespace': self.args.namespace, 'labels': {LABEL: self.args.namespace}},
                     'type': 'Opaque', 'stringData': {'fixture.json': json.dumps(fixture)}}, journal)
        write(self.args.results / 'cnpg-server-ready.json', {'namespace': self.args.database_namespace, 'namespace_uid': namespace['metadata']['uid'],
              'cluster_uid': created['metadata']['uid'], 'image': IMAGE, 'ready_instances': 2, 'storage_class': self.args.storage_class})

    def remove(self):
        self.require_lease()
        if self.get('job', 'service-check', self.args.namespace):
            raise RuntimeError('The application Job still exists; retain its database server')
        if not self.journal.exists():
            return
        journal = json.loads(self.journal.read_text())
        targets = [o for o in journal if o['kind'] == 'Namespace' or o['kind'] == 'Secret' and o['metadata'].get('namespace') == self.args.namespace]
        for item in reversed(targets):
            meta = item['metadata']
            current = self.get(item['kind'], meta['name'], meta.get('namespace'))
            if not current:
                continue
            if current['metadata']['uid'] != meta.get('uid') or current['metadata'].get('labels', {}).get(LABEL) != self.args.namespace or current['metadata'].get('annotations', {}).get('stego.test/cnpg-holder') != self.args.lease_holder:
                raise RuntimeError('CNPG cleanup refuses a different resource owner')
            path = '/api/v1/' + ('namespaces/' + meta['namespace'] + '/secrets/' if item['kind'] == 'Secret' else 'namespaces/') + meta['name']
            self.oc('delete', '--raw=' + path, '-f', '-', data={'apiVersion': 'v1', 'kind': 'DeleteOptions', 'preconditions': {'uid': meta['uid'], 'resourceVersion': current['metadata']['resourceVersion']}, 'propagationPolicy': 'Foreground'})
        deadline = time.monotonic() + 180
        while any(self.get(o['kind'], o['metadata']['name'], o['metadata'].get('namespace')) for o in targets):
            if time.monotonic() > deadline:
                raise RuntimeError('CNPG server cleanup is not complete; retain the operator and Lease')
            time.sleep(2)
        write(self.args.results / 'cnpg-server-cleanup.json', {'database_namespace_absent': self.args.database_namespace, 'private_fixture_absent': True})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['install', 'remove'])
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', default='stego-service-ci')
    parser.add_argument('--database-namespace', required=True)
    parser.add_argument('--storage-class', default='gp3-csi')
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--lease-holder', required=True)
    parser.add_argument('--lease-uid', required=True)
    args = parser.parse_args()
    validate(args.namespace, args.database_namespace, args.storage_class)
    getattr(Installation(args), args.action)()


if __name__ == '__main__':
    main()
