"""Build a fixed CNPG CI installation. Only the operator installs cluster policy."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import urllib.request

APP_NS = 'stego-service-ci'
OPERATOR_NS = 'stego-cnpg-operator-ci'
DATABASE_NS = 'stego-cnpg-database-ci'
OWNER = {'app.kubernetes.io/managed-by': 'stego-cnpg-ci'}
CONFIG = 'cnpg-ci-installation'
MANAGER_ROLE = 'stego-cnpg-ci-manager'


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def upstream():
    operator = module('cnpg_ci_upstream', 'cnpg-test-operator.py')
    with urllib.request.urlopen(operator.MANIFEST, timeout=30) as response:
        data = response.read(8 * 1024 * 1024 + 1)
    if hashlib.sha256(data).hexdigest() != operator.SHA256:
        raise RuntimeError('CNPG manifest checksum differs')
    return operator.manifest_documents(data)


def resource(kind, name, namespace=None, api='v1', **fields):
    metadata = {'name': name, 'labels': dict(OWNER)}
    if namespace:
        metadata['namespace'] = namespace
    return dict(apiVersion=api, kind=kind, metadata=metadata, **fields)


def binding(name, namespace, role, subjects, kind='Role'):
    return resource('RoleBinding', name, namespace, 'rbac.authorization.k8s.io/v1',
                    roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': kind, 'name': role}, subjects=subjects)


def service_account(name, namespace):
    return {'kind': 'ServiceAccount', 'name': name, 'namespace': namespace}


def endpoint_rules(endpoints):
    fixture = module('cnpg_ci_server_definitions', 'cnpg-installation-fixture.py')
    items = fixture.definitions(APP_NS, DATABASE_NS, 'gp3-csi', endpoints, operator_namespace=OPERATOR_NS)
    network = next(o for o in items if o['kind'] == 'NetworkPolicy')
    return [o for o in network['spec']['egress'] if any('ipBlock' in p for p in o.get('to', []))]


def build(documents, endpoints, issuer, storage_class):
    """Return static operator-owned objects and immutable run templates."""
    operator = module('cnpg_ci_operator_definitions', 'cnpg-test-operator.py')
    fixture = module('cnpg_ci_fixture_definitions', 'cnpg-installation-fixture.py')
    source = copy.deepcopy(documents)
    manager = next(o for o in source if o['kind'] == 'ClusterRole' and o['metadata']['name'] == 'cnpg-manager')
    local, global_read = [], []
    for rule in manager['rules']:
        names = rule['resources']
        local_names = [n for n in names if n not in {'nodes', 'clusterimagecatalogs', 'mutatingwebhookconfigurations', 'validatingwebhookconfigurations'}]
        global_names = [n for n in names if n in {'nodes', 'clusterimagecatalogs'}]
        if local_names:
            local.append(dict(rule, resources=local_names))
        if global_names:
            global_read.append(dict(rule, resources=global_names, verbs=['get', 'list', 'watch']))
    objects = []
    for ns in (OPERATOR_NS, DATABASE_NS):
        objects.append(resource('Namespace', ns))
        objects[-1]['metadata']['labels'].update({'pod-security.kubernetes.io/enforce': 'restricted', 'stego.test/cnpg-run': APP_NS})
    # CRDs have cluster scope. CI receives no permission to change them.
    for item in source:
        if item['kind'] == 'CustomResourceDefinition':
            item['metadata'].setdefault('labels', {}).update(OWNER)
            objects.append(item)
        elif item['kind'].endswith('WebhookConfiguration'):
            item['metadata']['name'] += '-stego-ci'
            item['metadata'].setdefault('labels', {}).update(OWNER)
            item['metadata'].setdefault('annotations', {})['cert-manager.io/inject-ca-from'] = OPERATOR_NS + '/webhook'
            for hook in item['webhooks']:
                hook['clientConfig']['service']['namespace'] = OPERATOR_NS
                hook['clientConfig'].pop('caBundle', None)
                hook['namespaceSelector'] = {'matchLabels': {'kubernetes.io/metadata.name': DATABASE_NS}}
                hook['timeoutSeconds'] = 5
                hook['failurePolicy'] = 'Fail'
            objects.append(item)
    objects += [
        resource('ClusterRole', MANAGER_ROLE, api='rbac.authorization.k8s.io/v1', rules=local),
        resource('ClusterRole', 'stego-cnpg-ci-observer', api='rbac.authorization.k8s.io/v1', rules=global_read),
        resource('ClusterRoleBinding', 'stego-cnpg-ci-observer', api='rbac.authorization.k8s.io/v1',
                 roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'ClusterRole', 'name': 'stego-cnpg-ci-observer'},
                 subjects=[service_account('cnpg-manager', OPERATOR_NS)]),
        resource('ServiceAccount', 'cnpg-manager', OPERATOR_NS, automountServiceAccountToken=True),
        binding('cnpg-manager', OPERATOR_NS, MANAGER_ROLE, [service_account('cnpg-manager', OPERATOR_NS)], 'ClusterRole'),
        resource('Service', 'cnpg-webhook-service', OPERATOR_NS,
                 spec={'selector': {'app.kubernetes.io/name': 'cloudnative-pg'}, 'ports': [{'port': 443, 'targetPort': 9443}]}),
        resource('Certificate', 'webhook', OPERATOR_NS, 'cert-manager.io/v1', spec={
            'secretName': 'cnpg-webhook-cert', 'duration': '48h', 'renewBefore': '12h',
            'dnsNames': ['cnpg-webhook-service.' + OPERATOR_NS + '.svc'],
            'issuerRef': {'kind': 'ClusterIssuer', 'name': issuer},
            'privateKey': {'rotationPolicy': 'Always'}}),
        resource('ResourceQuota', 'installation', OPERATOR_NS, spec={'hard': {
            'pods': '1', 'count/jobs.batch': '1', 'limits.cpu': '500m', 'limits.memory': '512Mi',
            'limits.ephemeral-storage': '128Mi', 'persistentvolumeclaims': '0'}}),
    ]
    server_objects = fixture.definitions(APP_NS, DATABASE_NS, storage_class, endpoints, operator_namespace=OPERATOR_NS)
    for item in server_objects:
        if item['kind'] in {'Namespace', 'Job', 'Cluster'}:
            continue
        item['metadata'].setdefault('labels', {}).update(OWNER)
        if item['kind'] == 'RoleBinding' and item['metadata']['name'] == 'cnpg-manager':
            item['roleRef']['name'] = MANAGER_ROLE
        objects.append(item)
    objects.append(resource('NetworkPolicy', 'default-deny', DATABASE_NS, 'networking.k8s.io/v1',
                            spec={'podSelector': {}, 'policyTypes': ['Ingress', 'Egress']}))
    # The operator owns this role. CI cannot change namespace access or policy.
    ci = [service_account('hypershell-ci', 'stego-ci-access')]
    for ns, rules in [(OPERATOR_NS, [
        {'apiGroups': ['batch'], 'resources': ['jobs'], 'verbs': ['get', 'list', 'create', 'delete']},
        {'apiGroups': [''], 'resources': ['pods'], 'verbs': ['get', 'list']},
    ]), (DATABASE_NS, [
        {'apiGroups': ['batch'], 'resources': ['jobs'], 'verbs': ['get', 'list', 'create', 'delete']},
        {'apiGroups': ['postgresql.cnpg.io'], 'resources': ['clusters'], 'verbs': ['get', 'list', 'create', 'delete']},
        {'apiGroups': [''], 'resources': ['secrets'], 'verbs': ['get', 'list', 'create', 'delete']},
        {'apiGroups': [''], 'resources': ['pods', 'persistentvolumeclaims', 'services', 'configmaps'], 'verbs': ['get', 'list', 'delete']},
        {'apiGroups': ['rbac.authorization.k8s.io'], 'resources': ['roles', 'rolebindings'], 'verbs': ['get', 'list']},
    ])]:
        objects.append(resource('Role', 'cnpg-ci', ns, 'rbac.authorization.k8s.io/v1', rules=rules))
        objects.append(binding('cnpg-ci', ns, 'cnpg-ci', ci))
    objects += [
        resource('Role', 'cnpg-ci-fixture', APP_NS, 'rbac.authorization.k8s.io/v1', rules=[
            {'apiGroups': [''], 'resources': ['configmaps'], 'resourceNames': [CONFIG], 'verbs': ['get']},
            {'apiGroups': [''], 'resources': ['secrets'], 'resourceNames': ['cnpg-credentials'], 'verbs': ['get', 'delete']},
            {'apiGroups': [''], 'resources': ['secrets'], 'verbs': ['create']},
        ]), binding('cnpg-ci-fixture', APP_NS, 'cnpg-ci-fixture', ci),
    ]
    objects += [
        resource('ClusterRole', 'stego-cnpg-ci-installation-reader', api='rbac.authorization.k8s.io/v1', rules=[
            {'apiGroups': [''], 'resources': ['namespaces'], 'resourceNames': [OPERATOR_NS, DATABASE_NS], 'verbs': ['get']},
            {'apiGroups': ['storage.k8s.io'], 'resources': ['storageclasses'], 'resourceNames': [storage_class], 'verbs': ['get']},
            {'apiGroups': [''], 'resources': ['persistentvolumes'], 'verbs': ['get', 'list']},
        ]),
        resource('ClusterRoleBinding', 'stego-cnpg-ci-installation-reader', api='rbac.authorization.k8s.io/v1',
                 roleRef={'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'ClusterRole', 'name': 'stego-cnpg-ci-installation-reader'}, subjects=ci),
    ]
    peers = [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': DATABASE_NS}}, 'podSelector': {'matchLabels': {'cnpg.io/cluster': 'gateway-database'}}}]
    objects.append(resource('NetworkPolicy', 'operator', OPERATOR_NS, 'networking.k8s.io/v1', spec={
        'podSelector': {}, 'policyTypes': ['Ingress', 'Egress'],
        # The managed control plane does not have namespace-selected Pods.
        # Only the TLS admission port is exposed; all outbound peers are bounded.
        'ingress': [{'ports': [{'port': 9443, 'protocol': 'TCP'}]}],
        'egress': endpoint_rules(endpoints) + [
            {'to': peers, 'ports': [{'port': 8000, 'protocol': 'TCP'}]},
            {'to': [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'openshift-dns'}}}],
             'ports': [{'port': 5353, 'protocol': 'UDP'}, {'port': 5353, 'protocol': 'TCP'}]},
        ]}))
    pod = next(o for o in source if o['kind'] == 'Deployment')['spec']['template']
    pod['metadata'].setdefault('labels', {})['app.kubernetes.io/name'] = 'cloudnative-pg'
    spec = pod['spec']
    spec['serviceAccountName'] = 'cnpg-manager'
    spec['automountServiceAccountToken'] = True
    spec['restartPolicy'] = 'Never'
    container = spec['containers'][0]
    container['image'] = operator.IMAGE
    container['args'] = [a.replace('--max-concurrent-reconciles=10', '--max-concurrent-reconciles=2') for a in container['args']]
    container['env'] = [{'name': k, 'value': v} for k, v in {
        'WATCH_NAMESPACE': DATABASE_NS, 'WEBHOOK_CERT_DIR': '/etc/cnpg-webhook',
        'MANAGE_WEBHOOK_CONFIGURATIONS': 'false', 'OPERATOR_NAMESPACE': OPERATOR_NS,
        'OPERATOR_IMAGE_NAME': operator.IMAGE, 'INHERITED_ANNOTATIONS': 'stego.test/cnpg-holder',
    }.items()]
    container['resources'] = {'requests': {'cpu': '100m', 'memory': '256Mi', 'ephemeral-storage': '32Mi'},
                              'limits': {'cpu': '500m', 'memory': '512Mi', 'ephemeral-storage': '128Mi'}}
    container['securityContext'] = {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}}
    spec['securityContext'] = {'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}}
    for volume in spec.get('volumes', []):
        if 'emptyDir' in volume:
            volume['emptyDir']['sizeLimit'] = '64Mi'
    spec.setdefault('volumes', []).append({'name': 'webhook-cert', 'secret': {'secretName': 'cnpg-webhook-cert',
        'items': [{'key': 'tls.crt', 'path': 'apiserver.crt'}, {'key': 'tls.key', 'path': 'apiserver.key'}]}})
    container.setdefault('volumeMounts', []).append({'name': 'webhook-cert', 'mountPath': '/etc/cnpg-webhook', 'readOnly': True})
    operator_job = resource('Job', 'cnpg-operator', OPERATOR_NS, 'batch/v1', spec={
        'activeDeadlineSeconds': 2400, 'backoffLimit': 0, 'ttlSecondsAfterFinished': 0, 'template': pod})
    database_job = next(o for o in server_objects if o['kind'] == 'Job')
    cluster = next(o for o in server_objects if o['kind'] == 'Cluster')
    templates = {'operator-job': operator_job, 'database-job': database_job, 'cluster': cluster}
    objects += policies(templates)
    return objects, templates


def policies(templates):
    base = json.loads((Path(__file__).resolve().parent.parent / 'deploy/ci/jshell.json').read_text())
    original = next(o for o in base['items'] if o['kind'] == 'ValidatingAdmissionPolicy' and o['metadata']['name'] == 'stego-ci-bounded-jobs')
    result = []
    for ns, key, maximum in [(OPERATOR_NS, 'operator-job', 2400), (DATABASE_NS, 'database-job', 1500)]:
        policy = copy.deepcopy(original)
        name = ns + '.bounded-jobs'
        policy['metadata'] = {'name': name, 'labels': dict(OWNER)}
        policy['spec']['matchConstraints']['namespaceSelector'] = {'matchLabels': {'kubernetes.io/metadata.name': ns}}
        policy['spec']['matchConditions'] = [{'name': 'ci-caller', 'expression': "request.userInfo.username == 'system:serviceaccount:stego-ci-access:hypershell-ci'"}]
        for variable in policy['spec']['variables']:
            if variable['name'] == 'secrets':
                variable['expression'] = '["cnpg-webhook-cert"]' if ns == OPERATOR_NS else '[]'
        for validation in policy['spec']['validations']:
            expression = validation['expression']
            if 'activeDeadlineSeconds' in expression:
                validation['expression'] = expression.replace('1200', str(maximum))
                validation['message'] = 'The CNPG test Job must have its bounded deadline'
            if ns == OPERATOR_NS and 'automountServiceAccountToken' in expression:
                validation['expression'] = 'has(variables.p.automountServiceAccountToken) && variables.p.automountServiceAccountToken'
            if ns == OPERATOR_NS and 'serviceAccountName' in expression:
                validation['expression'] = "has(variables.p.serviceAccountName) && variables.p.serviceAccountName == 'cnpg-manager'"
        rules = [
            (f'object.metadata.name == {json.dumps(templates[key]["metadata"]["name"])}', 'Use the fixed CNPG test Job name'),
            ('(!has(object.spec.suspend) || !object.spec.suspend)', 'The CNPG deadline must not be suspended'),
            ('(!has(object.spec.parallelism) || object.spec.parallelism == 1) && (!has(object.spec.completions) || object.spec.completions == 1)', 'Run one CNPG test Pod'),
            ('object.spec.ttlSecondsAfterFinished == 0', 'Remove the lifetime owner when its deadline ends'),
        ]
        if ns == OPERATOR_NS:
            container = templates[key]['spec']['template']['spec']['containers'][0]
            expected = {e['name']: e['value'] for e in container['env']}
            rules += [
                ('size(variables.p.containers) == 1 && (!has(variables.p.initContainers) || size(variables.p.initContainers) == 0)', 'The operator has one pinned container'),
                (f'variables.containers.all(c, c.image == {json.dumps(container["image"])} && c.command == {json.dumps(container["command"])} && c.args == {json.dumps(container["args"])})', 'Use the pinned CNPG operator command'),
                (f'variables.containers.all(c, has(c.env) && size(c.env) == {len(expected)} && c.env.all(e, e.name in {json.dumps(list(expected))} && has(e.value) && !has(e.valueFrom) && e.value == {json.dumps(expected)}[e.name]))', 'Keep the fixed namespace and external webhook trust settings'),
                ('variables.containers.all(c, !has(c.envFrom) && !has(c.lifecycle))', 'Do not add operator environment sources or lifecycle commands'),
                ('variables.containers.all(c, [c.livenessProbe, c.readinessProbe, c.startupProbe].all(p, has(p.httpGet) && !has(p.exec) && !has(p.tcpSocket) && !has(p.grpc) && p.httpGet.path == "/readyz" && p.httpGet.port == 9443 && p.httpGet.scheme == "HTTPS" && (!has(p.httpGet.host) || p.httpGet.host == "")))', 'Use only the fixed HTTPS operator health probes'),
                ('variables.containers.all(c, size(c.volumeMounts) == 3 && c.volumeMounts.all(m, !has(m.subPath) && !has(m.subPathExpr) && ((m.name == "scratch-data" && m.mountPath == "/controller") || (m.name == "webhook-certificates" && m.mountPath == "/run/secrets/cnpg.io/webhook") || (m.name == "webhook-cert" && m.mountPath == "/etc/cnpg-webhook" && m.readOnly))))', 'Keep the operator executable and configuration paths outside writable mounts'),
                ('size(variables.p.volumes) == 3 && variables.p.volumes.all(v, (v.name == "scratch-data" && has(v.emptyDir) && v.emptyDir.sizeLimit == "64Mi") || (v.name in ["webhook-certificates", "webhook-cert"] && has(v.secret) && v.secret.secretName == "cnpg-webhook-cert"))', 'Use only the fixed scratch volume and supplied webhook certificate'),
            ]
        else:
            container = templates[key]['spec']['template']['spec']['containers'][0]
            rules += [
                ('size(variables.p.containers) == 1 && (!has(variables.p.initContainers) || size(variables.p.initContainers) == 0)', 'The database lifetime Job has one pinned container'),
                (f'variables.containers.all(c, c.image == {json.dumps(container["image"])} && c.command == {json.dumps(container["command"])} && (!has(c.args) || size(c.args) == 0))', 'Use only the pinned database lifetime command'),
                ('variables.containers.all(c, !has(c.env) && !has(c.envFrom) && !has(c.lifecycle) && !has(c.livenessProbe) && !has(c.readinessProbe) && !has(c.startupProbe) && !has(c.volumeMounts)) && !has(variables.p.volumes)', 'The lifetime command needs no extra input, hooks, probes, or volumes'),
                ('variables.containers.all(c, c.resources.limits.cpu == "50m" && c.resources.limits.memory == "32Mi" && c.resources.limits["ephemeral-storage"] == "16Mi")', 'Keep the small lifetime container limits'),
                ('!has(object.spec.template.metadata) || !has(object.spec.template.metadata.labels) || !("cnpg.io/cluster" in object.spec.template.metadata.labels)', 'The lifetime Pod must not select database network permissions'),
            ]
        policy['spec']['validations'] += [{'expression': expression, 'message': message} for expression, message in rules]
        result += [policy, resource('ValidatingAdmissionPolicyBinding', name, api='admissionregistration.k8s.io/v1',
                                    spec={'policyName': name, 'validationActions': ['Deny']})]
    # Admission defaults can add fields. Check the bounded deployment inputs,
    # rather than comparing a submitted Cluster with an incomplete JSON shape.
    name = DATABASE_NS + '.bounded-cluster'
    cluster = templates['cluster']['spec']
    rules = [
        ("object.metadata.name == 'gateway-database'", 'Use the fixed database Cluster name'),
        ('object.spec.instances == 2', 'The test uses two database instances'),
        (f'object.spec.imageName == {json.dumps(cluster["imageName"])}', 'Use the pinned PostgreSQL image'),
        ('!has(object.spec.imageCatalogRef)', 'Do not replace the pinned PostgreSQL image with a catalog'),
        (f'object.spec.storage.size == "1Gi" && object.spec.storage.storageClass == {json.dumps(cluster["storage"]["storageClass"])}', 'Use the fixed test storage allocation'),
        ('has(object.spec.resources) && has(object.spec.resources.limits) && object.spec.resources.limits.cpu == "500m" && object.spec.resources.limits.memory == "768Mi" && object.spec.resources.limits["ephemeral-storage"] == "256Mi"', 'Keep the database resource limits'),
        ('has(object.metadata.ownerReferences) && size(object.metadata.ownerReferences) == 1 && object.metadata.ownerReferences[0].apiVersion == "batch/v1" && object.metadata.ownerReferences[0].kind == "Job" && object.metadata.ownerReferences[0].name == "database-lifetime"', 'The test Cluster requires its lifetime Job owner'),
    ]
    result += [resource('ValidatingAdmissionPolicy', name, api='admissionregistration.k8s.io/v1', spec={
        'failurePolicy': 'Fail',
        'matchConstraints': {'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': DATABASE_NS}},
            'resourceRules': [{'apiGroups': ['postgresql.cnpg.io'], 'apiVersions': ['v1'], 'operations': ['CREATE', 'UPDATE'], 'resources': ['clusters']}]},
        'matchConditions': [{'name': 'ci-caller', 'expression': "request.userInfo.username == 'system:serviceaccount:stego-ci-access:hypershell-ci'"}],
        'validations': [{'expression': expression, 'message': message} for expression, message in rules],
    }), resource('ValidatingAdmissionPolicyBinding', name, api='admissionregistration.k8s.io/v1',
                 spec={'policyName': name, 'validationActions': ['Deny']})]
    return result
