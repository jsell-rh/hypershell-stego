"""Check installation identity and API readback without cluster writes."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('ci_installation', Path(__file__).with_name('browser-ci-installation.py'))
installation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installation)


class InstallationInventory(unittest.TestCase):
    def fixture(self, count=25):
        items = [{'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
                  'metadata': {'name': installation.NAMESPACE + '.role-' + str(i)}, 'rules': []}
                 for i in range(count)]
        data = {name + '.json': json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': items if i == 0 else []})
                for i, name in enumerate(installation.MANIFESTS)}
        record = {'namespace': installation.NAMESPACE, 'resources': [
            {'kind': x['kind'], 'name': x['metadata']['name'], 'uid': 'uid-' + str(i)} for i, x in enumerate(items)]}
        return data, record, items

    def test_declared_inventory_has_no_fixed_count(self):
        for count in [1, 19, 25, 64]:
            data, record, items = self.fixture(count)
            expected, ids = installation.installation_inventory(record, data)
            self.assertEqual(len(expected), count)
            self.assertEqual(set(expected), set(ids))
            def get(kind, name):
                value = copy.deepcopy(expected[kind, name])
                value['metadata']['uid'] = ids[kind, name]
                return value
            installation.verify_installation_resources(record, data, get)

    def test_record_rejects_missing_extra_duplicate_and_invalid_identity(self):
        for mode in ['missing', 'extra', 'duplicate', 'uid', 'foreign', 'namespace', 'field']:
            data, record, _ = self.fixture()
            if mode == 'missing': record['resources'].pop()
            elif mode == 'extra': record['resources'].append({'kind': 'ClusterRole', 'name': installation.NAMESPACE + '.extra', 'uid': 'extra'})
            elif mode == 'duplicate': record['resources'][-1] = copy.deepcopy(record['resources'][0])
            elif mode == 'uid': record['resources'][0]['uid'] = ''
            elif mode == 'foreign': record['resources'][0]['name'] = 'other.role'
            elif mode == 'namespace': record['namespace'] = 'other'
            elif mode == 'field': record['resources'][0]['unexpected'] = True
            with self.subTest(mode=mode), self.assertRaises(RuntimeError):
                installation.installation_inventory(record, data)

    def test_manifest_rejects_invalid_and_repeated_identities(self):
        for mode in ['duplicate', 'cross-manifest', 'foreign', 'namespace', 'kind', 'version', 'unbounded', 'empty']:
            data, record, items = self.fixture()
            if mode == 'duplicate': items[-1] = copy.deepcopy(items[0])
            elif mode == 'cross-manifest': data[installation.MANIFESTS[1] + '.json'] = json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': [items[0]]})
            elif mode == 'foreign': items[0]['metadata']['name'] = 'other.role'
            elif mode == 'namespace': items[0]['metadata']['namespace'] = installation.NAMESPACE
            elif mode == 'kind': items[0]['kind'] = 'Secret'
            elif mode == 'version': items[0]['apiVersion'] = 'rbac.authorization.k8s.io/v2'
            elif mode == 'unbounded': items *= 3
            elif mode == 'empty': items.clear(); record['resources'].clear()
            data[installation.MANIFESTS[0] + '.json'] = json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': items})
            with self.subTest(mode=mode), self.assertRaises(RuntimeError):
                installation.installation_inventory(record, data)

    def test_replaced_or_changed_live_object_fails(self):
        for mode in ['uid', 'rules', 'read']:
            data, record, items = self.fixture(1)
            def get(kind, name):
                if mode == 'read': raise TimeoutError('test read failed')
                value = copy.deepcopy(items[0])
                value['metadata']['uid'] = 'replacement' if mode == 'uid' else 'uid-0'
                if mode == 'rules': value['rules'] = [{'verbs': ['*']}]
                return value
            with self.subTest(mode=mode), self.assertRaises((RuntimeError, TimeoutError)):
                installation.verify_installation_resources(record, data, get)

    def test_optional_variables_preserve_nonempty_values(self):
        policy = {'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingAdmissionPolicy',
                  'metadata': {'name': installation.NAMESPACE + '.policy'},
                  'spec': {'matchConstraints': {'resourceRules': []}, 'variables': None}}
        original = copy.deepcopy(policy)
        expected = installation.normalize(policy)
        self.assertEqual(policy, original)
        for value in [None, []]:
            policy['spec']['variables'] = value
            self.assertEqual(installation.normalize(policy), expected)
        policy['spec'].pop('variables')
        self.assertEqual(installation.normalize(policy), expected)
        for value in [[{'name': 'owner', 'expression': 'false'}], {}, False, '']:
            policy['spec']['variables'] = value
            self.assertNotEqual(installation.normalize(policy), expected)


if __name__ == '__main__':
    unittest.main()
