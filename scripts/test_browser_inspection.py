"""Check the fixture declaration and permission boundary without a cluster."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('inspection', Path(__file__).with_name('prepare-browser-inspection.py'))
inspection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inspection)
ROOT = Path(__file__).resolve().parent.parent


def go(config):
    return 'runtime before; json.Unmarshal([]byte(' + json.dumps(json.dumps(config)) + '), &config); runtime after'


class InspectionBoundary(unittest.TestCase):
    def test_endpoint_worker_change_is_limited_to_the_annotation(self):
        before = json.dumps({'stego.dev/allocation-network-endpoints': '{"gateway":["kubernetes"]}', 'replicas': 1})
        after = json.dumps({'stego.dev/allocation-network-endpoints': '{"gateway":["kubernetes","network-probe"]}', 'replicas': 1})
        inspection.verify_endpoint_worker_template(before, after)
        for invalid in [before, after.replace('"replicas": 1', '"replicas": 2'), after + after]:
            with self.assertRaises(ValueError):
                inspection.verify_endpoint_worker_template(before, invalid)

    def test_endpoint_change_declaration_adds_only_one_gateway_binding(self):
        original = (ROOT / 'service.yaml').read_text()
        changed = inspection.endpoint_change_declaration(original)
        self.assertEqual(changed.replace('[kubernetes, network-probe]', '[kubernetes]', 1), original)
        with self.assertRaises(ValueError):
            inspection.endpoint_change_declaration(changed)

    def test_endpoint_change_preserves_runtime_and_other_configuration(self):
        changed = copy.deepcopy(self.original)
        gateway = next(p for p in changed['Profiles'] if p['Name'] == 'gateway')
        gateway['NetworkEndpoints'].append('network-probe')
        inspection.verify_endpoint_change_runtime(go(self.original), go(changed))
        for alter in [lambda c: c['Profiles'][0]['NetworkEndpoints'].append('other'),
                      lambda c: c['Profiles'][0].update(NetworkIsolation=False),
                      lambda c: c['Profiles'][0]['Quota'].update(pods='50'),
                      lambda c: c['Roles'][0]['Rules'][0]['verbs'].append('delete')]:
            modified = copy.deepcopy(changed); alter(modified)
            with self.assertRaises(ValueError):
                inspection.verify_endpoint_change_runtime(go(self.original), go(modified))
        with self.assertRaises(ValueError):
            inspection.verify_endpoint_change_runtime(go(self.original), go(changed) + '; changed code')

    def setUp(self):
        self.original = inspection.allocation_config((ROOT / 'out/deploy/allocation/allocation.go').read_text())
        self.fixture = copy.deepcopy(self.original)
        self.fixture['Roles'] = inspection.inspection_roles() + self.fixture['Roles']
        for profile in self.fixture['Profiles']:
            role = {'gateway': 'fixture-gateway-inspector', 'gateway-state': 'fixture-state-inspector'}[profile['Name']]
            profile['Bindings'].append({'Role': role, 'ExternalRole': '', 'ServiceAccount': 'service-check', 'Namespace': 'control', 'ExternalNamespace': ''})

    def test_fixed_additions_preserve_runtime_and_original_bindings(self):
        self.assertEqual(inspection.verify_runtime(go(self.original), go(self.fixture)), inspection.inspection_roles())

    def test_undeclared_or_broader_permissions_are_rejected(self):
        changes = [
            lambda c: c['Roles'][0].update(Scope='cluster'),
            lambda c: c['Roles'][0]['Rules'][0].pop('resourceNames'),
            lambda c: c['Roles'][1]['Rules'][0]['verbs'].append('patch'),
            lambda c: c['Roles'][0]['Rules'].append({'apiGroups': ['*'], 'resources': ['*'], 'verbs': ['*']}),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(Namespace='allocated'),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(ServiceAccount='other'),
            lambda c: c['Profiles'][0]['Bindings'].reverse(),
            lambda c: c['Profiles'][0]['Quota'].update(pods='50'),
            lambda c: c['Roles'][2]['Rules'][0]['verbs'].append('delete'),
        ]
        for change in changes:
            config = copy.deepcopy(self.fixture)
            change(config)
            with self.subTest(change=change), self.assertRaises(ValueError):
                inspection.verify_runtime(go(self.original), go(config))

    def test_public_renewal_is_limited_to_one_status_resource(self):
        role = inspection.inspection_roles()[0]
        rules = [rule for rule in role['Rules'] if rule['apiGroups'] == ['cert-manager.io']]
        self.assertEqual(rules, [
            {'apiGroups': ['cert-manager.io'], 'resources': ['certificates'], 'verbs': ['get'], 'resourceNames': ['openshell-public-tls']},
            {'apiGroups': ['cert-manager.io'], 'resources': ['certificates/status'], 'verbs': ['update'], 'resourceNames': ['openshell-public-tls']},
        ])
        for change in [
            lambda r: r.pop('resourceNames'),
            lambda r: r['resourceNames'].append('openshell-server-tls'),
            lambda r: r.update(resources=['certificates']),
            lambda r: r['verbs'].append('delete'),
        ]:
            config = copy.deepcopy(self.fixture)
            target = next(rule for rule in config['Roles'][0]['Rules'] if rule['resources'] == ['certificates/status'])
            change(target)
            with self.assertRaises(ValueError):
                inspection.verify_runtime(go(self.original), go(config))

    def test_runtime_code_change_is_rejected(self):
        with self.assertRaises(ValueError):
            inspection.verify_runtime(go(self.original), go(self.fixture) + '; extra code')

    def test_declaration_is_additive_and_cannot_be_reapplied(self):
        original = (ROOT / 'service.yaml').read_text()
        fixture = inspection.declaration(original)
        self.assertIn(inspection.ROLES, fixture)
        restored = fixture.replace(inspection.ROLES, '', 1)
        for _, addition in inspection.BINDINGS:
            restored = restored.replace(addition, '', 1)
        self.assertEqual(restored, original)
        with self.assertRaises(ValueError):
            inspection.declaration(fixture)
        with self.assertRaises(ValueError):
            inspection.declaration(original.replace('    allocation_roles:', '    changed_roles:'))


    def test_unrelated_rendered_changes_are_rejected(self):
        base = 'stego-service-inspection.hypershell-namespace-allocation'
        original = {'items': [
            {'kind': 'ClusterRole', 'metadata': {'name': base}, 'rules': [{'verbs': ['bind'], 'resourceNames': [base + '.worker']}]},
            {'kind': 'ValidatingAdmissionPolicy', 'metadata': {'name': base + '.allocation'}, 'spec': {'validations': [
                {'expression': "request.resource.resource != 'rolebindings' || ORIGINAL", 'message': 'binding'},
                {'expression': 'KEEP_QUOTAS', 'message': 'quota'}]}},
            {'kind': 'Deployment', 'metadata': {'name': 'worker'}, 'spec': {'replicas': 1}},
        ]}
        fixture = copy.deepcopy(original)
        for role in inspection.inspection_roles():
            fixture['items'][0]['rules'][0]['resourceNames'].append(base + '.' + role['Name'])
            fixture['items'].append({'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
                'metadata': {'name': base + '.' + role['Name']}, 'rules': role['Rules']})
        fixture['items'][1]['spec']['validations'][0]['expression'] = "request.resource.resource != 'rolebindings' || FIXTURE"
        inspection.verify_manifests(json.dumps(original), json.dumps(fixture))
        changes = [
            lambda c: c['items'][1]['spec']['validations'][1].update(expression='true'),
            lambda c: c['items'][2]['spec'].update(replicas=20),
            lambda c: c['items'][3]['rules'][0].pop('resourceNames'),
            lambda c: c['items'].append(copy.deepcopy(c['items'][2])),
        ]
        for change in changes:
            changed = copy.deepcopy(fixture)
            change(changed)
            with self.subTest(change=change), self.assertRaises(ValueError):
                inspection.verify_manifests(json.dumps(original), json.dumps(changed))

    def test_cnpg_declaration_adds_one_private_namespace_peer(self):
        original = inspection.declaration((ROOT / 'service.yaml').read_text())
        fixture = inspection.cnpg_declaration(original, 'stego-cnpg-database-ci')
        extra = [line for line in fixture.splitlines(keepends=True) if line not in original.splitlines(keepends=True)]
        self.assertEqual(len(extra), 2)
        self.assertTrue(all('namespace: stego-cnpg-database-ci' in line for line in extra))
        restored = fixture
        for line in extra:
            restored = restored.replace(line, '', 1)
        self.assertEqual(restored, original)
        with self.assertRaises(ValueError):
            inspection.cnpg_declaration(fixture, 'stego-cnpg-database-ci')
        for namespace in ['default', '', 'stego-cnpg-database-', 'stego-cnpg-database-x/' , 'stego-cnpg-database-' + 'x' * 64]:
            with self.subTest(namespace=namespace), self.assertRaises(ValueError):
                inspection.cnpg_declaration(original, namespace)

    def test_cnpg_runtime_preserves_all_other_allocation_fields(self):
        original = inspection.allocation_config((ROOT / 'out/deploy/allocation/allocation.go').read_text())
        fixture = copy.deepcopy(original)
        gateway = next(p for p in fixture['Profiles'] if p['Name'] == 'gateway')
        peer = {'Direction': 'egress', 'Namespace': 'external', 'ExternalNamespace': 'stego-cnpg-database-ci', 'PodLabel': 'cnpg.io/cluster', 'PodValue': 'gateway-database', 'Protocol': 'TCP', 'Port': 5432}
        gateway['NetworkPeers'].insert(0, peer)
        inspection.verify_cnpg_runtime(go(original), go(fixture), 'stego-cnpg-database-ci')
        for key, value in [('Namespace', 'control'), ('ExternalNamespace', 'default'), ('Port', 0), ('PodValue', 'other')]:
            changed = copy.deepcopy(fixture)
            next(p for p in changed['Profiles'] if p['Name'] == 'gateway')['NetworkPeers'][0][key] = value
            with self.assertRaises(ValueError):
                inspection.verify_cnpg_runtime(go(original), go(changed), 'stego-cnpg-database-ci')
        with self.assertRaises(ValueError):
            inspection.verify_cnpg_runtime(go(original), go(fixture) + '; changed code', 'stego-cnpg-database-ci')

    def test_cnpg_render_cannot_broaden_network_or_worker_permissions(self):
        original = {'items': [
            {'kind': 'NetworkPolicy', 'metadata': {'name': 'hypershell-gateway-workload'},
             'spec': {'egress': [{'ports': [{'port': 9090, 'protocol': 'TCP'}]}]}},
            {'kind': 'Role', 'metadata': {'name': 'worker'}, 'rules': []},
        ]}
        fixture = copy.deepcopy(original)
        fixture['items'][0]['spec']['egress'].append({'ports': [{'port': 5432, 'protocol': 'TCP'}], 'to': [{
            'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'stego-cnpg-database-ci'}},
            'podSelector': {'matchLabels': {'cnpg.io/cluster': 'gateway-database'}}}]})
        inspection.verify_cnpg_manifests(json.dumps(original), json.dumps(fixture), 'stego-cnpg-database-ci')
        for change in [
            lambda d: d['items'][0]['spec']['egress'][-1]['to'][0].pop('podSelector'),
            lambda d: d['items'][0]['spec']['egress'][-1]['to'][0]['namespaceSelector'].update(matchLabels={}),
            lambda d: d['items'][0]['spec']['egress'][-1]['ports'][0].update(port=0),
            lambda d: d['items'][1]['rules'].append({'verbs': ['*'], 'resources': ['*']}),
            lambda d: d['items'][0]['spec']['egress'].append({'to': [{'ipBlock': {'cidr': '0.0.0.0/0'}}]}),
        ]:
            changed = copy.deepcopy(fixture)
            change(changed)
            with self.subTest(change=change), self.assertRaises(ValueError):
                inspection.verify_cnpg_manifests(json.dumps(original), json.dumps(changed), 'stego-cnpg-database-ci')


    def test_global_fixture_permissions_are_public_reads_only(self):
        document = json.loads((ROOT / 'acceptance/browser-workload-rbac.json').read_text())
        role, binding = document['items']
        self.assertEqual(role['kind'], 'ClusterRole')
        self.assertEqual(role['rules'], [
            {'apiGroups': [''], 'resources': ['namespaces'], 'verbs': ['get']},
            {'apiGroups': ['rbac.authorization.k8s.io'], 'resources': ['clusterroles'], 'verbs': ['get']},
            {'apiGroups': ['rbac.authorization.k8s.io'], 'resources': ['clusterrolebindings'], 'verbs': ['get', 'list']},
        ])
        self.assertEqual(binding['subjects'], [{'kind': 'ServiceAccount', 'name': 'service-check', 'namespace': '@NAMESPACE@'}])


class InspectionRecordBounds(unittest.TestCase):
    def test_writer_rejects_oversize_before_creating_a_record(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'record.json'
            with self.assertRaises(ValueError):
                inspection.write_inspection_record(target, {'padding': 'a' * inspection.INSPECTION_RECORD_LIMIT})
            self.assertFalse(target.exists())
            inspection.write_inspection_record(target, {'source_sha256': {'file': 'a' * (256 << 10)}})
            self.assertGreater(target.stat().st_size, 256 << 10)
            self.assertLess(target.stat().st_size, inspection.INSPECTION_RECORD_LIMIT)


if __name__ == '__main__':
    unittest.main()
