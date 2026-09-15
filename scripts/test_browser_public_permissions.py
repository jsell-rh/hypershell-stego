"""Check the exact public test permission change without a cluster."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import uuid

spec = importlib.util.spec_from_file_location('planner', Path(__file__).with_name('plan-browser-public-permissions.py'))
planner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(planner)


def document(items):
    return json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': items})


def fixture():
    roles = [
        {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
         'metadata': {'name': planner.PREFIX + suffix},
         'rules': [{'apiGroups': [''], 'resources': ['pods'], 'verbs': ['get']}]}
        for suffix in ['fixture-gateway-inspector', 'gateway-worker']]
    others = [{'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingAdmissionPolicy',
               'metadata': {'name': planner.NAMESPACE + '.policy-' + str(i)}, 'spec': {'failurePolicy': 'Fail'}}
              for i in range(16)]
    before = roles + others
    after = copy.deepcopy(before)
    after[0]['rules'] += [
        {'apiGroups': [''], 'resources': ['secrets'], 'verbs': ['get'], 'resourceNames': ['openshell-public-tls']},
        {'apiGroups': ['cert-manager.io'], 'resources': ['certificates'], 'verbs': ['get'], 'resourceNames': ['openshell-public-tls']},
        {'apiGroups': ['cert-manager.io'], 'resources': ['certificates/status'], 'verbs': ['update'], 'resourceNames': ['openshell-public-tls']},
    ]
    after[1]['rules'].append({'apiGroups': ['route.openshift.io'], 'resources': ['routes'], 'verbs': ['get', 'create', 'patch', 'delete']})
    old = {name: document([]) for name in planner.MANIFESTS}
    old['hypershell-namespace-allocation'] = document(before)
    new = {**old, 'hypershell-namespace-allocation': document(after)}
    record = {'namespace': planner.NAMESPACE, 'source_sha256': {'out/deploy/render/main.go': 'a' * 64},
              'manifests': {name: planner.digest(raw) for name, raw in old.items()},
              'resources': [{'kind': item['kind'], 'name': item['metadata']['name'], 'uid': str(uuid.UUID(int=i + 3))}
                            for i, item in enumerate(before)], 'policy_type_checks': 'success'}
    installation = {'apiVersion': 'v1', 'kind': 'ConfigMap', 'immutable': True,
                    'metadata': {'name': 'browser-ci-installation', 'namespace': planner.NAMESPACE,
                                 'uid': str(uuid.UUID(int=1)), 'resourceVersion': '42', 'labels': {'app.kubernetes.io/managed-by': 'stego-browser-ci'}},
                    'data': {name + '.json': raw for name, raw in old.items()}}
    installation['data'].update({'cluster-installation.json': json.dumps(record), 'namespace-uid': str(uuid.UUID(int=2)),
                                 'fs-group': '1000', 'issuer': 'test', 'kubernetes-endpoints.json': '{}', 'kubernetes-service.json': '{}'})
    rendered = {'namespace': planner.NAMESPACE, 'mode': 'render-only', 'resources': [],
                'source_sha256': record['source_sha256'], 'manifests': {name: planner.digest(raw) for name, raw in new.items()}}
    return installation, rendered, new


class PublicPermissionPlan(unittest.TestCase):
    def test_exact_additions_retain_installation_identity_and_other_data(self):
        installation, rendered, manifests = fixture()
        originals = copy.deepcopy((installation, rendered, manifests))
        plan = planner.plan(installation, rendered, manifests)
        self.assertEqual(plan['installation_uid'], installation['metadata']['uid'])
        self.assertEqual(plan['installation_resource_version'], '42')
        self.assertEqual(plan['namespace_uid'], installation['data']['namespace-uid'])
        self.assertEqual(len(plan['changes']), 2)
        self.assertEqual(plan['unchanged_resource_count'], 16)
        self.assertEqual(sum(len(c['added_grants']) for c in plan['changes']), 7)
        self.assertEqual(plan['proposed_installation_data']['issuer'], 'test')
        self.assertEqual(json.loads(plan['proposed_installation_data']['cluster-installation.json'])['policy_type_checks'], 'pending-live-verification')
        self.assertEqual((installation, rendered, manifests), originals)

    def test_broader_permissions_and_other_changes_fail(self):
        changes = [
            lambda items: items[0]['rules'][1].pop('resourceNames'),
            lambda items: items[0]['rules'][1]['resourceNames'].append('openshell-server-tls'),
            lambda items: items[0]['rules'][3].update(resources=['certificates']),
            lambda items: items[0]['rules'][3]['verbs'].append('delete'),
            lambda items: items[1]['rules'][1]['verbs'].append('watch'),
            lambda items: items[0]['rules'].pop(0),
            lambda items: items[0]['metadata'].update(labels={'changed': 'true'}),
            lambda items: items[0].update(aggregationRule={}),
            lambda items: items[0]['rules'].append(copy.deepcopy(items[0]['rules'][1])),
            lambda items: items[2]['spec'].update(failurePolicy='Ignore'),
            lambda items: items.pop(),
            lambda items: items.append(copy.deepcopy(items[0])),
            lambda items: items[2]['metadata'].update(name='foreign.policy'),
        ]
        for change in changes:
            with self.subTest(change=change):
                installation, rendered, manifests = fixture()
                items = json.loads(manifests['hypershell-namespace-allocation'])['items']
                change(items)
                manifests['hypershell-namespace-allocation'] = document(items)
                rendered['manifests']['hypershell-namespace-allocation'] = planner.digest(manifests['hypershell-namespace-allocation'])
                with self.assertRaises(ValueError):
                    planner.plan(installation, rendered, manifests)

    def test_invalid_saved_identity_and_hashes_fail(self):
        changes = [lambda i: i.update(immutable=False), lambda i: i['metadata'].update(uid=''),
                   lambda i: i['metadata'].update(namespace='foreign'), lambda i: i['metadata'].update(resourceVersion=42),
                   lambda i: i['metadata'].update(finalizers=['keep']), lambda i: i['metadata'].update(deletionTimestamp='now'),
                   lambda i: i['data'].update({'namespace-uid': 'bad'}), lambda i: i['data'].update({'fs-group': '0'}),
                   lambda i: i['data'].update({'extra': 'unreviewed'}),
                   lambda i: i['data'].update({'hypershell.json': document([{}])})]
        for change in changes:
            with self.subTest(change=change):
                installation, rendered, manifests = fixture()
                change(installation)
                with self.assertRaises(ValueError):
                    planner.plan(installation, rendered, manifests)

    def test_duplicate_uid_inventory_and_unverified_record_fail(self):
        for change in [lambda r: r['resources'].append(r['resources'][0]),
                       lambda r: r['resources'].pop(),
                       lambda r: r['resources'][1].update(uid=r['resources'][0]['uid']),
                       lambda r: r.update(policy_type_checks='pending'),
                       lambda r: r.update(extra='unexpected')]:
            installation, rendered, manifests = fixture()
            record = json.loads(installation['data']['cluster-installation.json'])
            change(record)
            installation['data']['cluster-installation.json'] = json.dumps(record)
            with self.assertRaises(ValueError):
                planner.plan(installation, rendered, manifests)

    def test_duplicate_json_and_oversize_input_fail(self):
        for raw in ['{"a":1,"a":2}', ' ' * ((2 << 20) + 1)]:
            with self.assertRaises(ValueError):
                planner.strict_json(raw)

    def test_command_writes_private_plan_once(self):
        installation, rendered, manifests = fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'cluster-manifests').mkdir()
            (root / 'installation.json').write_text(json.dumps(installation))
            (root / 'cluster-installation.json').write_text(json.dumps(rendered))
            for name, raw in manifests.items():
                (root / 'cluster-manifests' / (name + '.json')).write_text(raw)
            output = root / 'plan.json'
            arguments = ['planner', '--installation', str(root / 'installation.json'), '--rendered', str(root), '--output', str(output)]
            with patch('sys.argv', arguments):
                planner.main()
                self.assertEqual(os.stat(output).st_mode & 0o777, 0o600)
                self.assertEqual(json.loads(output.read_text())['kind'], 'BrowserPublicPermissionPlan')
                with self.assertRaises(FileExistsError):
                    planner.main()


if __name__ == '__main__':
    unittest.main()
