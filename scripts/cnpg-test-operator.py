#!/usr/bin/env python3
"""Install one bounded CNPG test operator. Retain an ownership journal for cleanup."""
import argparse
import hashlib
import json
import re
import subprocess
import time
import urllib.request
from pathlib import Path

MANIFEST = 'https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.30.0/cnpg-1.30.0.yaml'
SHA256 = 'f8bede43fe4ee0d478c2355b204a36876b2ae4faac60f2a9452280b293da3b88'
IMAGE = 'ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0@sha256:a2701eb97cdd2a34b1fdb2cb51987f544b706e40bec72ae7146cd8580efefebb'
LABEL = 'stego.test/cnpg-run'
OPERATOR_NS = 'cnpg-system'


def write(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def oc(context, *args, data=None):
    attempts = 3 if args and args[0] == 'get' else 1
    for attempt in range(attempts):
        try:
            result = subprocess.run(['oc', '--context=' + context, '--request-timeout=30s', *args],
                                    input=None if data is None else json.dumps(data),
                                    capture_output=True, text=True, timeout=40)
        except subprocess.TimeoutExpired:
            if attempt + 1 == attempts:
                raise
        else:
            if result.returncode == 0:
                return json.loads(result.stdout) if result.stdout.strip() else None
            transient = any(text in result.stderr for text in (
                'TLS handshake timeout', 'i/o timeout', 'connection reset',
                'unexpected EOF', 'context deadline exceeded', 'Client.Timeout'))
            if not transient or attempt + 1 == attempts:
                # The installer never handles credentials. Keep failures bounded.
                raise RuntimeError(result.stderr[-2000:])
        time.sleep(attempt + 1)



def get(context, obj):
    args = ['get', obj['kind'], obj['metadata']['name'], '--ignore-not-found', '-o', 'json']
    if 'namespace' in obj['metadata']:
        args += ['-n', obj['metadata']['namespace']]
    return oc(context, *args)


def validate_names(action, namespace, database_namespace):
    if not re.fullmatch(r'stego-(?:service|cnpg-live)-[a-z0-9](?:[a-z0-9-]*[a-z0-9])?', namespace) or len(namespace) > 63:
        raise ValueError('Use a dedicated service test namespace')
    if action == 'prepare':
        if not isinstance(database_namespace, str) or not re.fullmatch(r'stego-cnpg-database-[a-z0-9](?:[a-z0-9-]*[a-z0-9])?', database_namespace) or len(database_namespace) > 63:
            raise ValueError('Use an explicit installation database test namespace')
    elif database_namespace is not None:
        raise ValueError('--database-namespace applies only to prepare')


def manifest_documents(raw):
    import yaml
    return list(yaml.safe_load_all(raw))


def prepare(args):
    validate_names('prepare', args.namespace, args.database_namespace)
    path = args.evidence / 'cnpg-plan.json'
    if path.exists():
        raise RuntimeError('Use the existing CNPG plan; do not replace its ownership journal')
    with urllib.request.urlopen(MANIFEST, timeout=30) as response:
        raw = response.read(8 * 1024 * 1024 + 1)
    if hashlib.sha256(raw).hexdigest() != SHA256:
        raise RuntimeError('CNPG manifest checksum differs')
    documents = manifest_documents(raw)
    database_ns = args.database_namespace
    items = []
    role = next(o for o in documents if o['kind'] == 'ClusterRole' and o['metadata']['name'] == 'cnpg-manager')
    local_rules, global_rules = [], []
    for rule in role['rules']:
        cluster_resources = {'nodes', 'clusterimagecatalogs', 'mutatingwebhookconfigurations', 'validatingwebhookconfigurations'}
        for global_scope in (False, True):
            resources = [r for r in rule['resources'] if (r in cluster_resources) == global_scope]
            if not resources:
                continue
            split = dict(rule, resources=resources)
            if resources[0].endswith('webhookconfigurations'):
                split['resourceNames'] = ['cnpg-mutating-webhook-configuration', 'cnpg-validating-webhook-configuration']
            (global_rules if global_scope else local_rules).append(split)
    for obj in documents:
        kind = obj['kind']
        if kind == 'ClusterRole':
            if obj['metadata']['name'] != 'cnpg-manager':
                continue
            obj['rules'] = local_rules
        elif kind == 'ClusterRoleBinding':
            continue
        elif kind.endswith('WebhookConfiguration'):
            for hook in obj['webhooks']:
                hook['namespaceSelector'] = {'matchLabels': {'kubernetes.io/metadata.name': database_ns}}
                hook['timeoutSeconds'] = 5
        elif kind == 'Deployment':
            pod = obj['spec']['template']
            obj['spec']['replicas'] = 0
            container = pod['spec']['containers'][0]
            container['image'] = IMAGE
            container['args'] = [a.replace('--max-concurrent-reconciles=10', '--max-concurrent-reconciles=2') for a in container['args']]
            for entry in container['env']:
                if entry['name'] == 'OPERATOR_IMAGE_NAME':
                    entry['value'] = IMAGE
            container['env'].append({'name': 'WATCH_NAMESPACE', 'value': database_ns})
            container['startupProbe']['failureThreshold'] = 300
            container['resources'] = {'requests': {'cpu': '100m', 'memory': '256Mi', 'ephemeral-storage': '32Mi'}, 'limits': {'cpu': '500m', 'memory': '512Mi', 'ephemeral-storage': '128Mi'}}
            container['securityContext'].pop('runAsUser', None)
            container['securityContext'].pop('runAsGroup', None)
            for volume in pod['spec']['volumes']:
                if 'emptyDir' in volume:
                    volume['emptyDir']['sizeLimit'] = '64Mi'
        obj['metadata'].setdefault('labels', {})[LABEL] = args.namespace
        if kind == 'Namespace':
            obj['metadata']['labels']['pod-security.kubernetes.io/enforce'] = 'restricted'
        items.append(obj)
    subject = [{'kind': 'ServiceAccount', 'name': 'cnpg-manager', 'namespace': OPERATOR_NS}]
    def resource(kind, name, namespace=None, **fields):
        meta = {'name': name, 'labels': {LABEL: args.namespace}}
        if namespace:
            meta['namespace'] = namespace
        return dict(apiVersion='rbac.authorization.k8s.io/v1', kind=kind, metadata=meta, **fields)
    items += [
        resource('ClusterRole', 'cnpg-test-observer', rules=global_rules),
        resource('ClusterRoleBinding', 'cnpg-test-observer', subjects=subject, roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'ClusterRole', 'name': 'cnpg-test-observer'}),
        resource('RoleBinding', 'cnpg-manager', OPERATOR_NS, subjects=subject, roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'ClusterRole', 'name': 'cnpg-manager'}),
        resource('Role', 'cnpg-test-start', OPERATOR_NS, rules=[{'apiGroups':['batch'], 'resources':['jobs'], 'resourceNames':['cnpg-test-lifetime'], 'verbs':['get','patch']}, {'apiGroups':['apps'], 'resources':['deployments'], 'resourceNames':['cnpg-controller-manager'], 'verbs':['get','patch']}]),
        resource('RoleBinding', 'cnpg-test-start', OPERATOR_NS, subjects=[{'kind':'ServiceAccount','name':'service-check','namespace':args.namespace}], roleRef={'apiGroup':'rbac.authorization.k8s.io','kind':'Role','name':'cnpg-test-start'}),
        {'apiVersion': 'v1', 'kind': 'ResourceQuota', 'metadata': {'name': 'cnpg-test', 'namespace': OPERATOR_NS, 'labels': {LABEL: args.namespace}}, 'spec': {'hard': {'pods': '2', 'limits.cpu': '550m', 'limits.memory': '544Mi', 'limits.ephemeral-storage': '144Mi'}}},
    ]
    # CNPG requires a Deployment for certificate ownership. A separate Job owns
    # that Deployment. Its deadline and TTL also stop CNPG if the host exits.
    items.append({'apiVersion': 'batch/v1', 'kind': 'Job',
        'metadata': {'name': 'cnpg-test-lifetime', 'namespace': OPERATOR_NS, 'labels': {LABEL: args.namespace}},
        'spec': {'suspend': True, 'backoffLimit': 0, 'activeDeadlineSeconds': 1800, 'ttlSecondsAfterFinished': 0,
            'template': {'metadata': {'labels': {LABEL: args.namespace}}, 'spec': {
                'restartPolicy': 'Never', 'automountServiceAccountToken': False,
                'securityContext': {'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}},
                'containers': [{'name': 'deadline',
                    'image': 'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393',
                    'command': ['/bin/sleep', '3600'],
                    'securityContext': {'readOnlyRootFilesystem': True, 'allowPrivilegeEscalation': False, 'capabilities': {'drop': ['ALL']}},
                    'resources': {'requests': {'cpu': '5m', 'memory': '8Mi', 'ephemeral-storage': '1Mi'},
                        'limits': {'cpu': '50m', 'memory': '32Mi', 'ephemeral-storage': '16Mi'}}}]}}}})
    # Install limits and permissions first, then the lifetime Job and Deployment.
    items.sort(key=lambda o: {'Namespace': 0, 'Job': 2, 'Deployment': 3}.get(o['kind'], 1))
    for obj in items:
        if get(args.context, obj):
            raise RuntimeError('CNPG test refuses an existing resource: ' + obj['kind'] + '/' + obj['metadata']['name'])
    write(path, {'namespace': args.namespace, 'database_namespace': database_ns, 'manifest_sha256': SHA256, 'items': items})
    write(args.evidence / 'cnpg-created.json', [])


def install(args):
    plan = json.loads((args.evidence / 'cnpg-plan.json').read_text())
    journal = args.evidence / 'cnpg-created.json'
    created = json.loads(journal.read_text())
    if created:
        raise RuntimeError('CNPG installation has started; inspect the existing operator')
    for obj in plan['items']:
        if obj['kind'] == 'Deployment':
            lifetime = next(o for o in created if o['kind'] == 'Job' and o['metadata']['name'] == 'cnpg-test-lifetime')
            obj['metadata']['ownerReferences'] = [{'apiVersion': 'batch/v1', 'kind': 'Job',
                'name': lifetime['metadata']['name'], 'uid': lifetime['metadata']['uid']}]
        # Create, never apply: do not change a resource installed by another actor.
        result = oc(args.context, 'create', '-f', '-', '-o', 'json', data=obj)
        created.append({'apiVersion': obj['apiVersion'], 'kind': obj['kind'], 'metadata': {k: result['metadata'][k] for k in ('name', 'namespace', 'uid') if k in result['metadata']}})
        write(journal, created)


def remove(args):
    path = args.evidence / 'cnpg-created.json'
    if not path.exists():
        return
    created = json.loads(path.read_text())
    # The caller removes the installation database namespace while this operator
    # still runs. Gateway controllers never own this namespace.
    plan = json.loads((args.evidence / 'cnpg-plan.json').read_text())
    db = {'kind': 'Namespace', 'metadata': {'name': plan['database_namespace']}}
    if get(args.context, db):
        raise RuntimeError('Database namespace still exists; keep CNPG for its finalizers')
    for obj in reversed(created):
        current = get(args.context, obj)
        if not current:
            continue
        if current['metadata']['uid'] != obj['metadata']['uid'] or current['metadata'].get('labels', {}).get(LABEL) != plan['namespace']:
            raise RuntimeError('CNPG cleanup refuses a changed resource owner')
        if obj['kind'] == 'CustomResourceDefinition':
            instances = oc(args.context, 'get', obj['metadata']['name'], '-A', '-o', 'json')
            if instances.get('items'):
                raise RuntimeError('CNPG cleanup refuses a CRD that still has resources')
        version = obj['apiVersion']
        prefix = '/api/v1' if version == 'v1' else '/apis/' + version
        resources = {'Namespace': 'namespaces', 'CustomResourceDefinition': 'customresourcedefinitions', 'ClusterRole': 'clusterroles', 'ClusterRoleBinding': 'clusterrolebindings', 'Role': 'roles', 'RoleBinding': 'rolebindings', 'ServiceAccount': 'serviceaccounts', 'Service': 'services', 'ConfigMap': 'configmaps', 'ResourceQuota': 'resourcequotas', 'Job': 'jobs', 'Deployment': 'deployments', 'MutatingWebhookConfiguration': 'mutatingwebhookconfigurations', 'ValidatingWebhookConfiguration': 'validatingwebhookconfigurations'}
        if 'namespace' in obj['metadata']:
            prefix += '/namespaces/' + obj['metadata']['namespace']
        target = prefix + '/' + resources[obj['kind']] + '/' + obj['metadata']['name']
        oc(args.context, 'delete', '--raw=' + target, '-f', '-', data={'apiVersion': 'v1', 'kind': 'DeleteOptions', 'preconditions': {'uid': obj['metadata']['uid'], 'resourceVersion': current['metadata']['resourceVersion']}, 'propagationPolicy': 'Background'})
    write(args.evidence / 'cnpg-cleanup-requested.json', {'resources': len(created)})


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['prepare', 'install', 'remove'])
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', required=True)
    parser.add_argument('--evidence', required=True, type=Path)
    parser.add_argument('--database-namespace', help='Dedicated installation database namespace; prepare only')
    args = parser.parse_args()
    validate_names(args.action, args.namespace, args.database_namespace)
    globals()[args.action](args)
