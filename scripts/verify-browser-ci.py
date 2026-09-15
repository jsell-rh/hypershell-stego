#!/usr/bin/env python3
"""Check browser CI permissions and admission without starting a Job."""
import argparse
import copy
import importlib.util
import json
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--results', required=True, type=Path)
    args = parser.parse_args()
    namespace = 'stego-service-ci'
    prefix = ['oc', '--context=' + args.context, '--request-timeout=20s']

    def request(words, body=None):
        return subprocess.run(prefix + words, input=None if body is None else json.dumps(body), capture_output=True, text=True, timeout=30)

    identity = request(['whoami'])
    if identity.returncode or identity.stdout.strip() != 'system:serviceaccount:stego-ci-access:hypershell-ci':
        raise RuntimeError('The browser CI identity differs')
    module_spec = importlib.util.spec_from_file_location('fixture', Path(__file__).with_name('render-service-fixture.py'))
    module = importlib.util.module_from_spec(module_spec); module_spec.loader.exec_module(module)
    fixture = module.fixture(namespace, args.results, '1', '1', (args.results / 'issuer').read_text().strip())
    base = next(o for o in fixture['items'] if o['kind'] == 'Job')
    probes = []
    for name in ['bounded', 'parallel', 'completions', 'no_deadline', 'long_deadline', 'retry', 'foreign_identity', 'foreign_name', 'privileged', 'writable_root', 'no_limits']:
        job = copy.deepcopy(base); spec = job['spec']; pod = spec['template']['spec']; container = pod['containers'][0]
        if name == 'parallel': spec['parallelism'] = 2
        if name == 'completions': spec['completions'] = 2
        if name == 'no_deadline': spec.pop('activeDeadlineSeconds')
        if name == 'long_deadline': spec['activeDeadlineSeconds'] = 1801
        if name == 'retry': spec['backoffLimit'] = 1
        if name == 'foreign_identity': pod['serviceAccountName'] = 'hypershell-namespace-allocation'
        if name == 'foreign_name': job['metadata']['name'] = 'other-check'
        if name == 'privileged':
            container['securityContext']['privileged'] = True
            container['securityContext']['allowPrivilegeEscalation'] = True
        if name == 'writable_root': container['securityContext']['readOnlyRootFilesystem'] = False
        if name == 'no_limits': container.pop('resources')
        result = request(['create', '--dry-run=server', '-f', '-', '-o', 'name'], job)
        allowed = name == 'bounded'
        policy = namespace + '.stego-ci-bounded-jobs'
        if (result.returncode == 0) != allowed or (not allowed and policy not in result.stderr):
            raise RuntimeError('Browser CI admission probe failed: ' + name + '\n' + result.stderr)
        probes.append({'case': name, 'allowed': allowed, 'denial_source': None if allowed else policy})
    secret = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'database-tls', 'namespace': namespace, 'annotations': {'kubernetes.io/service-account.name': 'service-check'}}, 'type': 'kubernetes.io/service-account-token'}
    result = request(['create', '--dry-run=server', '-f', '-', '-o', 'name'], secret)
    if result.returncode == 0 or namespace + '.stego-ci-no-legacy-tokens' not in result.stderr:
        raise RuntimeError('Browser CI accepted a lasting token Secret')
    probes.append({'case': 'lasting_token', 'allowed': False, 'denial_source': namespace + '.stego-ci-no-legacy-tokens'})
    denied = [
        ('foreign_secret', ['-n', 'default', 'get', 'secret', 'browser-ci-denied-probe', '-o', 'name'], None),
        ('cluster_role', ['create', '--dry-run=server', '-f', '-', '-o', 'name'], {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole', 'metadata': {'name': namespace + '.denied-probe'}, 'rules': []}),
        ('installation_write', ['-n', namespace, 'patch', 'configmap', 'browser-ci-installation', '--dry-run=server', '--type=merge', '-p', '{"metadata":{"labels":{"probe":"denied"}}}', '-o', 'name'], None),
    ]
    for name, words, body in denied:
        result = request(words, body)
        if result.returncode == 0 or 'Forbidden' not in result.stderr or 'cannot' not in result.stderr:
            raise RuntimeError('Browser CI permission probe failed: ' + name)
        probes.append({'case': name, 'allowed': False, 'denial_source': 'RBAC'})
    (args.results / 'ci-access.json').write_text(json.dumps({'identity': identity.stdout.strip(), 'checks': probes, 'live_jobs_created': 0}, indent=2) + '\n')
    print('Browser CI access checks passed: ' + str(len(probes)))


if __name__ == '__main__':
    main()
