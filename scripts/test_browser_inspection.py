"""Check the fixture declaration and permission boundary without a cluster."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('inspection', Path(__file__).with_name('prepare-browser-inspection.py'))
inspection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inspection)
ROOT = Path(__file__).resolve().parent.parent


def go(config):
    return 'runtime before; json.Unmarshal([]byte(' + json.dumps(json.dumps(config)) + '), &config); runtime after'


class InspectionBoundary(unittest.TestCase):
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


if __name__ == '__main__':
    unittest.main()
