"""Check the fixture declaration and permission boundary without a cluster."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

import sandbox_network_fixture as sandbox_network

spec = importlib.util.spec_from_file_location('inspection', Path(__file__).with_name('prepare-browser-inspection.py'))
inspection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inspection)
ROOT = Path(__file__).resolve().parent.parent


def go(config):
    return 'runtime before; json.Unmarshal([]byte(' + json.dumps(json.dumps(config)) + '), &config); runtime after'


class InspectionBoundary(unittest.TestCase):
    def test_native_network_declaration_changes_only_the_sandbox_class(self):
        source = (ROOT / 'service.yaml').read_text()
        fixture = sandbox_network.declaration(source)
        self.assertEqual(fixture.replace(sandbox_network.RUNTIME_CLASS, 'kata', 1), source)
        self.assertFalse(sandbox_network.record()['vm_isolation_tested'])
        for invalid in [fixture,
                        source.replace('      - name: sandbox\n', '      - name: other\n'),
                        source.replace('        pod_runtime_class: kata\n', ''),
                        source.replace('        pod_security: isolated-runtime\n', '        pod_security: restricted\n'),
                        source.replace('        pod_runtime_class: kata\n', '        pod_runtime_class: kata\n' * 2),
                        source + '      - name: sandbox\n']:
            with self.subTest(source=invalid[-80:]), self.assertRaises(ValueError):
                sandbox_network.declaration(invalid)

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

    def test_endpoint_change_keeps_account_fields_and_rejects_ambiguous_profiles(self):
        source = (ROOT / 'service.yaml').read_text()
        accounts = '        service_accounts: [gateway, console]\n'
        self.assertIn(accounts, source)
        for original in [source, source.replace(accounts, '', 1), source.replace(accounts, '        # Account names belong to the allocator.\n' + accounts, 1)]:
            changed = inspection.endpoint_change_declaration(original)
            self.assertEqual(changed.replace('[kubernetes, network-probe]', '[kubernetes]', 1), original)
        invalid = [
            source.replace('      - name: gateway\n', '      - name: other\n', 1),
            source.replace('      - name: gateway-state\n        network_isolation:', '      - name: gateway\n        network_isolation:', 1),
            source.replace('        network_endpoints: [kubernetes]\n', '', 1),
            source.replace('        network_endpoints: [kubernetes]\n', '        network_endpoints: [kubernetes]\n' * 2, 1),
            source.replace('        network_isolation: true\n', '        network_isolation: false\n', 1),
        ]
        for original in invalid:
            with self.subTest(original=original[:40]), self.assertRaises(ValueError):
                inspection.endpoint_change_declaration(original)

    def test_account_inspection_is_read_only_and_namespace_scoped(self):
        role = inspection.inspection_roles()[0]
        accounts = [r for r in role['Rules'] if 'serviceaccounts' in r['resources']]
        self.assertEqual(role['Scope'], 'namespace')
        self.assertEqual(accounts, [{'apiGroups': [''], 'resources': ['serviceaccounts'], 'verbs': ['get']}])
        deployments = [r for r in role['Rules'] if r['resources'] == ['deployments']]
        self.assertEqual(deployments, [{'apiGroups': ['apps'], 'resources': ['deployments'], 'verbs': ['get', 'list', 'watch'], 'resourceNames': ['hypershell-gateway-console', 'openshell-gateway']}])
        fixture = copy.deepcopy(self.fixture)
        next(r for r in fixture['Roles'][0]['Rules'] if r['resources'] == ['serviceaccounts'])['verbs'].append('create')
        with self.assertRaises(ValueError):
            inspection.verify_runtime(go(self.original), go(fixture))

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
            role = {'gateway': 'fixture-gateway-inspector', 'gateway-state': 'fixture-state-inspector', 'gateway-console-state': 'fixture-console-state-inspector', 'sandbox': 'fixture-sandbox-inspector'}.get(profile['Name'])
            if role is None:
                continue
            profile['Bindings'].append({'Role': role, 'ExternalRole': '', 'ServiceAccount': 'service-check', 'Namespace': 'control', 'ExternalNamespace': '', 'SubjectProfile': '', 'SubjectPrefix': ''})

    def test_console_state_inspection_checks_isolation_with_read_only_grants(self):
        original = next(p for p in self.original['Profiles'] if p['Name'] == 'gateway-console-state')
        fixture = next(p for p in self.fixture['Profiles'] if p['Name'] == 'gateway-console-state')
        self.assertEqual(original['Bindings'], fixture['Bindings'][:-1])
        role = next(r for r in self.fixture['Roles'] if r['Name'] == 'fixture-console-state-inspector')
        self.assertEqual(role['Rules'], [
            {'apiGroups': [''], 'resources': ['secrets'], 'resourceNames': ['gateway-console-state'], 'verbs': ['get']},
            {'apiGroups': [''], 'resources': ['resourcequotas'], 'resourceNames': ['stego-allocation'], 'verbs': ['get']},
            {'apiGroups': ['networking.k8s.io'], 'resources': ['networkpolicies'], 'resourceNames': ['stego-allocation'], 'verbs': ['get']},
            {'apiGroups': ['networking.k8s.io'], 'resources': ['networkpolicies'], 'verbs': ['list']},
        ])
        self.assertEqual(fixture['Bindings'][-1]['Role'], role['Name'])
        fixture['Bindings'].append(copy.deepcopy(self.fixture['Profiles'][0]['Bindings'][-1]))
        with self.assertRaises(ValueError):
            inspection.verify_runtime(go(self.original), go(self.fixture))

    def test_fixed_additions_preserve_runtime_and_original_bindings(self):
        self.assertEqual(inspection.verify_runtime(go(self.original), go(self.fixture)), inspection.inspection_roles())

    def test_sandbox_inspector_cannot_read_credentials_or_change_accounts(self):
        role = next(r for r in self.fixture['Roles'] if r['Name'] == 'fixture-sandbox-inspector')
        self.assertEqual(role['Scope'], 'namespace')
        self.assertEqual({r for rule in role['Rules'] for r in rule['resources']},
                         {'resourcequotas', 'networkpolicies', 'pods', 'pods/log', 'serviceaccounts', 'rolebindings'})
        accounts = next(rule for rule in role['Rules'] if rule['resources'] == ['serviceaccounts'])
        self.assertEqual(accounts['verbs'], ['get'])
        for change in [lambda r: r['Rules'].append({'apiGroups': [''], 'resources': ['secrets'], 'verbs': ['get']}),
                       lambda r: next(rule for rule in r['Rules'] if rule['resources'] == ['serviceaccounts'])['verbs'].append('create'),
                       lambda r: r.update(Scope='cluster')]:
            config = copy.deepcopy(self.fixture)
            change(next(r for r in config['Roles'] if r['Name'] == role['Name']))
            with self.assertRaises(ValueError):
                inspection.verify_runtime(go(self.original), go(config))

    def test_undeclared_or_broader_permissions_are_rejected(self):
        changes = [
            lambda c: c['Roles'][0].update(Scope='cluster'),
            lambda c: c['Roles'][0]['Rules'][0].pop('resourceNames'),
            lambda c: c['Roles'][1]['Rules'][0]['verbs'].append('patch'),
            lambda c: c['Roles'][0]['Rules'].append({'apiGroups': ['*'], 'resources': ['*'], 'verbs': ['*']}),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(Namespace='allocated'),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(ServiceAccount='other'),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(SubjectProfile='gateway'),
            lambda c: c['Profiles'][0]['Bindings'][-1].update(SubjectPrefix='foreign-'),
            lambda c: c['Profiles'][0]['Bindings'].reverse(),
            lambda c: c['Profiles'][0]['Quota'].update(pods='50'),
            lambda c: c['Roles'][2]['Rules'][0]['verbs'].append('delete'),
            lambda c: c['Roles'][2]['Rules'][1].pop('resourceNames'),
            lambda c: c['Roles'][2]['Rules'][2]['verbs'].append('patch'),
            lambda c: c['Roles'][2]['Rules'][3]['resources'].append('secrets'),
            lambda c: c['Roles'][2]['Rules'].pop(),
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
        template = (ROOT / 'out/deploy/render/worker-namespace-allocation.json.tmpl').read_text()
        rendered = json.loads(template.replace('{{.FSGroup}}', '1').replace('{{.Namespace}}', 'stego-service-inspection'))
        guard = next(item for item in rendered['items'] if item['kind'] == 'ValidatingAdmissionPolicy' and item['metadata']['name'] == base + '.control-accounts')
        original['items'].append(guard)
        original['items'].append({'kind': 'ValidatingAdmissionPolicy',
            'metadata': {'name': base + '.pods.sandbox'},
            'spec': {'failurePolicy': 'Fail', 'validations': [
                {'expression': sandbox_network.EXPRESSION + '"kata"'},
                {'expression': 'KEEP_POD_GUARDS'}]}})
        fixture = copy.deepcopy(original)
        names = json.dumps(['hypershell-gateway-workload', 'hypershell-namespace-allocation', 'hypershell-sandbox-count'], separators=(',', ':'))
        extended = names[:-1] + ',"service-check"]'
        fixture['items'][3]['spec']['validations'][0]['expression'] = guard['spec']['validations'][0]['expression'].replace(names, extended)
        for role in inspection.inspection_roles():
            fixture['items'][0]['rules'][0]['resourceNames'].append(base + '.' + role['Name'])
            fixture['items'].append({'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
                'metadata': {'name': base + '.' + role['Name']}, 'rules': role['Rules']})
        fixture['items'][1]['spec']['validations'][0]['expression'] = "request.resource.resource != 'rolebindings' || FIXTURE"
        inspection.verify_manifests(json.dumps(original), json.dumps(fixture))
        native = copy.deepcopy(fixture)
        rules = native['items'][4]['spec']['validations']
        rules[0]['expression'] = sandbox_network.EXPRESSION + json.dumps(sandbox_network.RUNTIME_CLASS)
        restored = sandbox_network.restore_manifest(json.dumps(native))
        inspection.verify_manifests(json.dumps(original), restored)
        with self.assertRaises(ValueError):
            inspection.verify_manifests(json.dumps(original), json.dumps(native))
        for change in [
            lambda c: c['items'][4]['spec'].update(failurePolicy='Ignore'),
            lambda c: c['items'][4]['spec']['validations'][1].update(expression='true'),
            lambda c: c['items'][2]['spec'].update(replicas=20),
            lambda c: c['items'][5]['rules'][0]['verbs'].append('delete'),
        ]:
            modified = copy.deepcopy(native)
            change(modified)
            with self.assertRaises(ValueError):
                inspection.verify_manifests(json.dumps(original), sandbox_network.restore_manifest(json.dumps(modified)))
        for change in [
            lambda c: c['items'].pop(4),
            lambda c: c['items'].append(copy.deepcopy(c['items'][4])),
            lambda c: c['items'][4]['spec']['validations'].append(copy.deepcopy(c['items'][4]['spec']['validations'][0])),
            lambda c: c['items'][4]['spec']['validations'][0].update(expression='true'),
        ]:
            modified = copy.deepcopy(native)
            change(modified)
            with self.assertRaises(ValueError):
                sandbox_network.restore_manifest(json.dumps(modified))
        changes = [
            lambda c: c['items'][1]['spec']['validations'][1].update(expression='true'),
            lambda c: c['items'][2]['spec'].update(replicas=20),
            lambda c: c['items'][5]['rules'][0].pop('resourceNames'),
            lambda c: c['items'][3]['spec'].update(failurePolicy='Ignore'),
            lambda c: c['items'][3]['spec']['validations'][0].update(expression='true'),
            lambda c: c['items'][3]['spec']['validations'][0].update(expression=guard['spec']['validations'][0]['expression']),
            lambda c: c['items'][3]['spec']['validations'][0].update(expression=c['items'][3]['spec']['validations'][0]['expression'].replace('service-check', 'other')),
            lambda c: c['items'][3]['spec']['matchConditions'][0].update(expression='false'),
            lambda c: c['items'].pop(3),
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
