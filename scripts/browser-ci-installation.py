#!/usr/bin/env python3
"""Check the operator's immutable browser CI installation before use."""
import argparse
import base64
import copy
import json
import os
from pathlib import Path
import subprocess
from ci_credentials import BROWSER_SECONDS, require_credentials

NAMESPACE = 'stego-service-ci'
MANIFESTS = ['hypershell', 'hypershell-console', 'hypershell-provisioner', 'hypershell-namespace-allocation', 'hypershell-gateway-identity', 'hypershell-gateway-workload']


def normalize(item):
    value = copy.deepcopy(item)
    value.pop('status', None)
    value['metadata'] = {k: value['metadata'][k] for k in ['name', 'namespace'] if k in value['metadata']}
    if value['kind'] == 'ValidatingAdmissionPolicy':
        # Kubernetes omits an empty optional variable list on readback.
        if value['spec'].get('variables') in (None, []):
            value['spec'].pop('variables', None)
        match = value['spec']['matchConstraints']
        match.setdefault('matchPolicy', 'Equivalent')
        match.setdefault('namespaceSelector', {})
        match.setdefault('objectSelector', {})
        for rule in match['resourceRules']:
            rule.setdefault('scope', '*')
    if value['kind'] == 'ClusterRoleBinding':
        for subject in value['subjects']:
            if subject['kind'] == 'ServiceAccount':
                subject.setdefault('apiGroup', '')
    if value['kind'] == 'NetworkPolicy':
        # The API omits empty rule lists. Keep policyTypes and every rule exact.
        value['spec'].setdefault('ingress', [])
        value['spec'].setdefault('egress', [])
    return value


def installation_inventory(record, data):
    """Match the complete bounded manifest set to its recorded identities."""
    if record.get('namespace') != NAMESPACE or not isinstance(record.get('resources'), list):
        raise RuntimeError('The cluster installation record differs')
    expected = {}
    for name in MANIFESTS:
        document = json.loads(data[name + '.json'])
        if (not isinstance(document, dict) or document.get('apiVersion') != 'v1' or
                document.get('kind') != 'List' or not isinstance(document.get('items'), list) or
                len(document['items']) > 64):
            raise RuntimeError('The cluster manifest inventory differs')
        for item in document['items']:
            if not isinstance(item, dict) or not isinstance(item.get('metadata'), dict):
                raise RuntimeError('A cluster manifest identity is invalid')
            kind, name = item.get('kind'), item['metadata'].get('name')
            versions = {'ClusterRole': 'rbac.authorization.k8s.io/v1',
                        'ClusterRoleBinding': 'rbac.authorization.k8s.io/v1',
                        'ValidatingAdmissionPolicy': 'admissionregistration.k8s.io/v1',
                        'ValidatingAdmissionPolicyBinding': 'admissionregistration.k8s.io/v1'}
            if (not isinstance(kind, str) or kind not in versions or item.get('apiVersion') != versions[kind] or
                    not isinstance(name, str) or not name.startswith(NAMESPACE + '.') or
                    'namespace' in item['metadata'] or (kind, name) in expected):
                raise RuntimeError('A cluster manifest identity is invalid or repeated')
            expected[kind, name] = item
    if not expected or len(record['resources']) != len(expected):
        raise RuntimeError('The cluster installation inventory differs')
    ids = {}
    for item in record['resources']:
        if (not isinstance(item, dict) or set(item) != {'kind', 'name', 'uid'} or
                not all(isinstance(item[key], str) and item[key] for key in ['kind', 'name', 'uid'])):
            raise RuntimeError('A recorded cluster identity is invalid')
        key = item['kind'], item['name']
        if key not in expected or key in ids:
            raise RuntimeError('A recorded cluster identity is unexpected or repeated')
        ids[key] = item['uid']
    if ids.keys() != expected.keys():
        raise RuntimeError('The cluster installation inventory differs')
    return expected, ids


def verify_installation_resources(record, data, get):
    expected, ids = installation_inventory(record, data)
    for key, item in expected.items():
        actual = get(*key)
        if actual['metadata']['uid'] != ids[key] or normalize(actual) != normalize(item):
            raise RuntimeError('An installed cluster resource changed: ' + key[0] + '/' + key[1])


def verify_fixture_network(fixture, get):
    names = set()
    for expected in fixture['items']:
        if expected['kind'] != 'NetworkPolicy':
            continue
        name = expected['metadata']['name']
        if name in names or expected['metadata']['namespace'] != NAMESPACE:
            raise RuntimeError('The fixture network policy identity differs')
        names.add(name)
        actual = get('networkpolicy', name, NAMESPACE)
        if (actual['metadata'].get('labels', {}).get('app.kubernetes.io/managed-by') != 'stego-browser-ci' or
                normalize(actual) != normalize(expected)):
            raise RuntimeError('The installed fixture network policy differs: ' + name)
    if not names:
        raise RuntimeError('The fixture has no receiver network policy')


def fixture_document(root, directory, issuer):
    import importlib.util
    spec = importlib.util.spec_from_file_location('fixture', root / 'scripts/render-service-fixture.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.fixture(NAMESPACE, directory, '1', '1', issuer)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['inspect', 'prepare', 'verify', 'cleanup'])
    parser.add_argument('--context', required=True)
    parser.add_argument('--results', required=True, type=Path)
    args = parser.parse_args()
    if args.action == 'inspect':
        # Preflight runs before prepare in the same result directory. Preserve
        # its evidence without creating prepare's manifests or cleanup journal.
        args.results = args.results / 'installation-inspection'
        args.results.mkdir(mode=0o700)
    root = Path(__file__).resolve().parent.parent
    def oc(*words):
        return subprocess.run(['oc', '--context=' + args.context, '--request-timeout=20s', *words], capture_output=True, check=True, timeout=30).stdout
    def get(kind, name, namespace=None):
        words = ['get', kind, name, '-o', 'json']
        if namespace:
            words += ['-n', namespace]
        return json.loads(oc(*words))
    config = json.loads(oc('config', 'view', '--raw', '--minify', '-o', 'json'))
    token, cluster = require_credentials(config, BROWSER_SECONDS if args.action == 'prepare' else 0)
    installation = get('configmap', 'browser-ci-installation', NAMESPACE)
    if installation.get('immutable') is not True or installation['metadata'].get('labels', {}).get('app.kubernetes.io/managed-by') != 'stego-browser-ci':
        raise RuntimeError('The browser CI manifest record is not owned and immutable')
    data = installation['data']
    names = {name + '.json' for name in MANIFESTS} | {'namespace-uid', 'fs-group', 'issuer', 'kubernetes-endpoints.json', 'kubernetes-service.json', 'cluster-installation.json'}
    if set(data) != names:
        raise RuntimeError('The installation record has unexpected fields')
    namespace = get('namespace', NAMESPACE)
    if namespace['metadata']['uid'] != data['namespace-uid'] or namespace['metadata'].get('labels', {}).get('app.kubernetes.io/managed-by') != 'stego-browser-ci':
        raise RuntimeError('The browser CI namespace has a different identity')
    if not data['fs-group'].isdigit() or int(data['fs-group']) < 1 or data['fs-group'] != namespace['metadata']['annotations']['openshift.io/sa.scc.supplemental-groups'].split('/')[0]:
        raise RuntimeError('The browser CI group changed')
    record = json.loads(data['cluster-installation.json'])
    verify_installation_resources(record, data, get)
    if args.action in {'inspect', 'prepare'}:
        for name in ['kubernetes-endpoints.json', 'kubernetes-service.json']:
            (args.results / name).write_text(data[name])
        (args.results / 'operator-cluster-installation.json').write_text(data['cluster-installation.json'])
        (args.results / 'fs-group').write_text(data['fs-group'] + '\n')
        (args.results / 'issuer').write_text(data['issuer'] + '\n')
        (args.results / 'installation.json').write_text(json.dumps(installation, indent=2) + '\n')
        subprocess.run(['python3', str(root / 'scripts/prepare-browser-cluster.py'), '--context', args.context, '--namespace', NAMESPACE,
                        '--fs-group', data['fs-group'], '--results', str(args.results), '--workload', '--render-only'], cwd=root, check=True, timeout=150)
        for name in MANIFESTS:
            if (args.results / 'cluster-manifests' / (name + '.json')).read_bytes() != data[name + '.json'].encode():
                raise RuntimeError('Generated cluster policy differs from the operator installation: ' + name)
        (args.results / 'cluster-installation.json').write_text(data['cluster-installation.json'])
        # Each run starts with the original, narrow test Role. RBAC escalation
        # checks limit the CI identity to permissions it already has here.
        fixture = fixture_document(root, args.results, data['issuer'])
        verify_fixture_network(fixture, get)
        if args.action == 'inspect':
            print('Browser CI installation inspected without cluster writes')
            return
        desired = next(i for i in fixture['items'] if i['kind'] == 'Role' and i['metadata']['name'] == 'service-check')
        current = get('role', 'service-check', NAMESPACE)
        patch = [{'op': 'test', 'path': '/metadata/uid', 'value': current['metadata']['uid']}, {'op': 'test', 'path': '/metadata/resourceVersion', 'value': current['metadata']['resourceVersion']}, {'op': 'replace', 'path': '/rules', 'value': desired['rules']}]
        subprocess.run(['oc', '--context=' + args.context, '--request-timeout=20s', '-n', NAMESPACE, 'patch', 'role', 'service-check', '--type=json', '-p', json.dumps(patch)], capture_output=True, check=True, timeout=30)
        # The cleanup client gets only the short-lived CI token, never operator
        # credentials. Its allocator token is requested through the scoped Role.
        private = args.results / 'ci-token'
        with private.open('x') as output:
            os.fchmod(output.fileno(), 0o600); output.write(token)
        ca = args.results / 'ci-ca.pem'
        if cluster.get('certificate-authority-data'):
            ca.write_bytes(base64.b64decode(cluster['certificate-authority-data'], validate=True))
        elif cluster.get('certificate-authority'):
            ca.write_bytes(Path(cluster['certificate-authority']).read_bytes())
        else:
            ca.write_text('')
        (args.results / 'ci-server').write_text(cluster['server'] + '\n')
        environment = dict(os.environ, GOMAXPROCS='1', GOMEMLIMIT='256MiB', GOWORK='off')
        subprocess.run(['go', 'build', '-p=1', '-mod=readonly', '-trimpath', '-o', str(args.results / 'allocation-cleanup'), 'scripts/browser-allocation-cleanup.go'], cwd=root, env=environment, check=True, timeout=60)
    if args.action == 'verify':
        verify_fixture_network(fixture_document(root, args.results, data['issuer']), get)
        for name in MANIFESTS:
            if (args.results / 'cluster-manifests' / (name + '.json')).read_bytes() != data[name + '.json'].encode():
                raise RuntimeError('Generated cluster policy differs from the operator installation')
        (args.results / 'cluster-installation.json').write_text(data['cluster-installation.json'])
    if args.action in {'prepare', 'cleanup'}:
        ca = args.results / 'ci-ca.pem'
        command = [str(args.results / 'allocation-cleanup'), '--namespace', NAMESPACE, '--server', (args.results / 'ci-server').read_text().strip(),
                   '--ca-file', str(ca) if ca.stat().st_size else '', '--token-file', str(args.results / 'ci-token'),
                   '--result', str(args.results / ('allocation-cleanup.json' if args.action == 'cleanup' else 'allocation-preflight.json'))]
        if args.action == 'cleanup':
            command.append('--remove')
        from kubernetes_endpoint_bindings import kubernetes_endpoints
        environment = dict(os.environ, STEGO_ALLOCATION_NETWORK_ENDPOINTS=json.dumps({"kubernetes": kubernetes_endpoints(args.results)}))
        subprocess.run(command, check=True, timeout=195, env=environment)
    print('Browser CI installation checked: ' + args.action)


if __name__ == '__main__':
    main()
