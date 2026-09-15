#!/usr/bin/env python3
"""Install the fixed browser CI namespace and generated cluster policy.

Run once from a prepared inspection source with the operator context. CI can
read this installation, but cannot replace its cluster policy or manifest map.
"""
import argparse
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import time

NAMESPACE = 'stego-service-ci'
OWNER = {'app.kubernetes.io/managed-by': 'stego-browser-ci'}


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def ci_objects(fixture, policies):
    objects = [copy.deepcopy(o) for o in fixture['items'] if o['kind'] in {'ResourceQuota', 'NetworkPolicy', 'ServiceAccount', 'Role', 'RoleBinding', 'ClusterRole', 'ClusterRoleBinding'}]
    driver = next(o for o in objects if o['kind'] == 'Role' and o['metadata']['name'] == 'service-check')
    rules = copy.deepcopy(driver['rules'])
    for rule in rules:
        if rule['resources'] == ['pods/exec']:
            rule.pop('resourceNames', None)
    rules += [
        {'apiGroups': ['batch'], 'resources': ['jobs'], 'verbs': ['get', 'list', 'watch', 'create', 'delete']},
        {'apiGroups': ['rbac.authorization.k8s.io'], 'resources': ['roles'], 'resourceNames': ['service-check'], 'verbs': ['get', 'patch', 'update']},
        {'apiGroups': [''], 'resources': ['configmaps'], 'verbs': ['get', 'list', 'create']},
        {'apiGroups': [''], 'resources': ['configmaps'], 'resourceNames': ['database-ca'], 'verbs': ['patch', 'delete']},
    ]
    objects += [
        {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'Role', 'metadata': {'name': 'browser-ci', 'namespace': NAMESPACE}, 'rules': rules},
        {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'RoleBinding', 'metadata': {'name': 'browser-ci', 'namespace': NAMESPACE},
         'roleRef': {'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'Role', 'name': 'browser-ci'},
         'subjects': [{'kind': 'ServiceAccount', 'name': 'hypershell-ci', 'namespace': 'stego-ci-access'}]},
    ]
    for item in objects:
        if item['kind'] == 'ClusterRole':
            names = [NAMESPACE + '.hypershell-namespace-allocation.' + suffix for suffix in ['allocation', 'ownership', 'resources']]
            item['rules'].append({'apiGroups': ['admissionregistration.k8s.io'], 'resources': ['validatingadmissionpolicies', 'validatingadmissionpolicybindings'], 'resourceNames': names, 'verbs': ['get']})
        if item['kind'] == 'ClusterRoleBinding':
            item['subjects'].append({'kind': 'ServiceAccount', 'name': 'hypershell-ci', 'namespace': 'stego-ci-access'})
        if item['kind'] == 'ResourceQuota':
            item['spec']['hard'].update({'count/jobs.batch': '1', 'persistentvolumeclaims': '0'})
    for original in policies['items']:
        if original['kind'] not in {'ValidatingAdmissionPolicy', 'ValidatingAdmissionPolicyBinding'}:
            continue
        item = copy.deepcopy(original)
        name = NAMESPACE + '.' + original['metadata']['name']
        item['metadata']['name'] = name
        if item['kind'] == 'ValidatingAdmissionPolicyBinding':
            item['spec']['policyName'] = name
        else:
            item['spec']['matchConstraints']['namespaceSelector'] = {'matchLabels': {'kubernetes.io/metadata.name': NAMESPACE}}
            if original['metadata']['name'] == 'stego-ci-bounded-jobs':
                for check in item['spec']['validations']:
                    expression = check['expression']
                    if 'activeDeadlineSeconds' in expression:
                        check['expression'] = expression.replace('1200', '1800')
                        check['message'] = 'Browser CI Jobs require a deadline of at most 1800 seconds'
                    if 'automountServiceAccountToken' in expression:
                        check['expression'] = 'has(object.spec.template.spec.automountServiceAccountToken) && object.spec.template.spec.automountServiceAccountToken'
                        check['message'] = 'Browser CI uses its fixed test identity'
                    if 'serviceAccountName' in expression:
                        check['expression'] = "has(object.spec.template.spec.serviceAccountName) && object.spec.template.spec.serviceAccountName == 'service-check'"
                        check['message'] = 'Browser CI requires the service-check identity'
                item['spec']['validations'].append({'expression': "object.metadata.name == 'service-check'", 'message': 'Browser CI permits only its fixed Job name'})
                item['spec']['validations'].append({'expression': '(!has(object.spec.parallelism) || object.spec.parallelism == 1) && (!has(object.spec.completions) || object.spec.completions == 1)', 'message': 'Browser CI runs one test Pod'})
        objects.append(item)
    for item in objects:
        item['metadata'].setdefault('labels', {}).update(OWNER)
    return objects


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--issuer', required=True)
    parser.add_argument('--results', type=Path, required=True)
    args = parser.parse_args()
    source = Path(__file__).resolve().parent.parent
    if not (source / 'acceptance/browser-inspection-source.json').is_file():
        raise RuntimeError('Use a prepared inspection source')
    args.results.mkdir(mode=0o700, parents=True, exist_ok=False)
    def oc(*words, data=None):
        return subprocess.run(['oc', '--context=' + args.context, '--request-timeout=20s', *words], input=data, capture_output=True, check=True, timeout=30).stdout
    if oc('get', 'namespace', NAMESPACE, '--ignore-not-found', '-o', 'json').strip():
        raise RuntimeError('The browser CI namespace already exists; do not replace its installation')
    issuer = json.loads(oc('get', 'clusterissuer', args.issuer, '-o', 'json'))
    if not any(c.get('type') == 'Ready' and c.get('status') == 'True' for c in issuer.get('status', {}).get('conditions', [])):
        raise RuntimeError('The existing ClusterIssuer is not ready')
    lock = module('live_lock', source / 'scripts/jshell_live_lock.py')
    holder = 'browser-ci-installation'
    lock.acquire(args.context, holder, NAMESPACE, 'installation')
    record = {'namespace': NAMESPACE, 'resources': [], 'complete': False}
    def save():
        (args.results / 'installation.json').write_text(json.dumps(record, indent=2) + '\n')
    def create(item):
        meta = item['metadata']; words = ['get', item['kind'], meta['name'], '--ignore-not-found', '-o', 'json']
        if meta.get('namespace'):
            words += ['-n', meta['namespace']]
        if oc(*words).strip():
            raise RuntimeError('An installation resource already exists')
        value = json.loads(oc('create', '-f', '-', '-o', 'json', data=json.dumps(item).encode()))
        record['resources'].append({'kind': value['kind'], 'name': meta['name'], 'namespace': meta.get('namespace', ''), 'uid': value['metadata']['uid']})
        save()
        return value
    try:
        for resource, file in [('endpointslices', 'kubernetes-endpoints.json'), ('service', 'kubernetes-service.json')]:
            words = ['-n', 'default', 'get', resource]
            words += ['-l', 'kubernetes.io/service-name=kubernetes'] if resource == 'endpointslices' else ['kubernetes']
            (args.results / file).write_bytes(oc(*words, '-o', 'json'))
        fixture = module('service_fixture', source / 'scripts/render-service-fixture.py').fixture(NAMESPACE, args.results, '1', '1', args.issuer)
        namespace = create({'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': NAMESPACE, 'labels': {**OWNER, 'pod-security.kubernetes.io/enforce': 'restricted', 'pod-security.kubernetes.io/audit': 'restricted', 'pod-security.kubernetes.io/warn': 'restricted'}}})
        for _ in range(30):
            namespace = json.loads(oc('get', 'namespace', NAMESPACE, '-o', 'json'))
            group = namespace['metadata'].get('annotations', {}).get('openshift.io/sa.scc.supplemental-groups', '').split('/')[0]
            if group.isdigit() and 0 < int(group) < 2**31:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError('OpenShift did not assign the fixture group')
        objects = ci_objects(fixture, json.loads((source / 'deploy/ci/jshell.json').read_text()))
        # Policy is active before CI receives its RoleBinding.
        objects.sort(key=lambda item: item['kind'] not in {'ValidatingAdmissionPolicy', 'ValidatingAdmissionPolicyBinding'})
        for item in objects:
            create(item)
        for item in objects:
            if item['kind'] != 'ValidatingAdmissionPolicy':
                continue
            for _ in range(30):
                current = json.loads(oc('get', item['kind'], item['metadata']['name'], '-o', 'json'))
                status = current.get('status', {})
                if status.get('observedGeneration') == current['metadata']['generation'] and 'typeChecking' in status:
                    if status['typeChecking'].get('expressionWarnings'):
                        raise RuntimeError('CI admission policy type checking failed')
                    break
                time.sleep(0.5)
            else:
                raise RuntimeError('CI admission policy type checking did not finish')
        subprocess.run(['python3', str(source / 'scripts/prepare-browser-cluster.py'), '--context', args.context, '--namespace', NAMESPACE,
                        '--fs-group', group, '--results', str(args.results), '--workload'], cwd=source, check=True, timeout=240)
        create({'apiVersion': 'v1', 'kind': 'ServiceAccount', 'metadata': {'name': 'hypershell-namespace-allocation', 'namespace': NAMESPACE, 'labels': OWNER}, 'automountServiceAccountToken': False})
        data = {'namespace-uid': namespace['metadata']['uid'], 'fs-group': group, 'issuer': args.issuer,
                'kubernetes-endpoints.json': (args.results / 'kubernetes-endpoints.json').read_text(),
                'kubernetes-service.json': (args.results / 'kubernetes-service.json').read_text(),
                'cluster-installation.json': (args.results / 'cluster-installation.json').read_text()}
        for path in sorted((args.results / 'cluster-manifests').glob('*.json')):
            data[path.name] = path.read_text()
        create({'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'browser-ci-installation', 'namespace': NAMESPACE, 'labels': OWNER}, 'immutable': True, 'data': data})
        record['complete'] = True; save()
        print('Installed the fixed browser CI namespace and its immutable manifest record.')
    finally:
        # Installation creates no Job or Pod. Keep its journal on any failure.
        lock.release(args.context, holder)


if __name__ == '__main__':
    main()
