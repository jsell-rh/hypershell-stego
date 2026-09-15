"""Check the fixed CI permissions and manifest drift detection."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent

def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

fixture = load('fixture', 'render-service-fixture.py')
setup = load('setup', 'prepare-browser-ci.py')
installation = load('installation', 'browser-ci-installation.py')

class BrowserCI(unittest.TestCase):
    def objects(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'kubernetes-endpoints.json').write_text(json.dumps({'items': [{'endpoints': [{'conditions': {'ready': True}, 'addresses': ['192.0.2.1']}], 'ports': [{'protocol': 'TCP', 'name': 'https', 'port': 443}]}]}))
            (root / 'kubernetes-service.json').write_text(json.dumps({'spec': {'clusterIP': '10.0.0.1', 'clusterIPs': ['10.0.0.1']}}))
            body = fixture.fixture(setup.NAMESPACE, root, '1', '1', 'test-ca')
            return body, setup.ci_objects(body, json.loads((ROOT / 'deploy/ci/jshell.json').read_text()))

    def test_ci_does_not_get_cluster_mutation_rights(self):
        _, objects = self.objects()
        for role in [o for o in objects if o['kind'] == 'ClusterRole']:
            for rule in role['rules']:
                self.assertLessEqual(set(rule['verbs']), {'get', 'list'})
                self.assertFalse(set(rule['resources']) & {'secrets', 'pods', 'tokenreviews'})
        ci = next(o for o in objects if o['kind'] == 'Role' and o['metadata']['name'] == 'browser-ci')
        rbac = [r for r in ci['rules'] if r['apiGroups'] == ['rbac.authorization.k8s.io']]
        self.assertEqual(rbac, [{'apiGroups': ['rbac.authorization.k8s.io'], 'resources': ['roles'], 'resourceNames': ['service-check'], 'verbs': ['get', 'patch', 'update']}])
        for rule in ci['rules']:
            self.assertFalse(set(rule['verbs']) & {'bind', 'escalate', 'impersonate', '*'})
            if rule['resources'] == ['configmaps'] and set(rule['verbs']) & {'patch', 'delete'}:
                self.assertEqual(rule['resourceNames'], ['database-ca'])

    def test_driver_cannot_change_its_own_permissions(self):
        _, objects = self.objects()
        driver = next(o for o in objects if o['kind'] == 'Role' and o['metadata']['name'] == 'service-check')
        self.assertFalse(any('rbac.authorization.k8s.io' in r['apiGroups'] for r in driver['rules']))
        exec_rule = next(r for r in driver['rules'] if r['resources'] == ['pods/exec'])
        self.assertEqual(exec_rule['resourceNames'], ['identity-fixture'])
        for role in [o for o in objects if o['kind'] == 'Role']:
            rules = [r for r in role['rules'] if 'serviceaccounts' in r['resources']]
            self.assertEqual(len(rules), 1)
            self.assertNotIn('delete', rules[0]['verbs'])

    def test_jobs_and_storage_are_bounded(self):
        body, objects = self.objects()
        quota = next(o for o in objects if o['kind'] == 'ResourceQuota')['spec']['hard']
        self.assertEqual(quota['count/jobs.batch'], '1')
        self.assertEqual(quota['persistentvolumeclaims'], '0')
        job = next(o for o in body['items'] if o['kind'] == 'Job')['spec']
        self.assertLessEqual(job['ttlSecondsAfterFinished'], 3600)
        pod = job['template']['spec']
        self.assertIs(pod['securityContext']['runAsNonRoot'], True)
        self.assertEqual(pod['securityContext']['seccompProfile'], {'type': 'RuntimeDefault'})
        for container in pod['containers'] + pod['initContainers']:
            self.assertLessEqual({'cpu', 'memory', 'ephemeral-storage'}, container['resources']['limits'].keys())
        policy = next(o for o in objects if o['kind'] == 'ValidatingAdmissionPolicy' and o['metadata']['name'].endswith('bounded-jobs'))
        expressions = [c['expression'] for c in policy['spec']['validations']]
        self.assertIn("object.metadata.name == 'service-check'", expressions)
        self.assertTrue(any("serviceAccountName == 'service-check'" in e for e in expressions))
        self.assertTrue(any('activeDeadlineSeconds <= 1800' in e for e in expressions))

    def test_mutable_fixture_data_has_an_owner_label(self):
        body, _ = self.objects()
        for item in body['items']:
            if item['kind'] in {'Secret', 'ConfigMap', 'Service', 'Job'}:
                self.assertEqual(item['metadata']['labels']['stego.test/browser-run'], setup.NAMESPACE)

    def test_normalization_preserves_permission_changes(self):
        expected = {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole', 'metadata': {'name': 'test'}, 'rules': [{'apiGroups': [''], 'resources': ['secrets'], 'resourceNames': ['one'], 'verbs': ['get']}]}
        actual = copy.deepcopy(expected); actual['metadata'].update(uid='uid', resourceVersion='1')
        self.assertEqual(installation.normalize(actual), expected)
        actual['rules'][0]['verbs'].append('list')
        self.assertNotEqual(installation.normalize(actual), expected)
        actual = copy.deepcopy(expected); actual['aggregationRule'] = {'clusterRoleSelectors': [{}]}
        self.assertNotEqual(installation.normalize(actual), expected)

    def test_policy_defaults_do_not_hide_selector_changes(self):
        expected = {'kind': 'ValidatingAdmissionPolicy', 'metadata': {'name': 'test'}, 'spec': {'matchConstraints': {'resourceRules': [{'resources': ['namespaces']}]}, 'validations': [{'expression': 'true'}]}}
        actual = copy.deepcopy(expected)
        actual['spec']['matchConstraints'].update(matchPolicy='Equivalent', namespaceSelector={}, objectSelector={})
        actual['spec']['matchConstraints']['resourceRules'][0]['scope'] = '*'
        actual['status'] = {'observedGeneration': 1}
        self.assertEqual(installation.normalize(actual), installation.normalize(expected))
        actual['spec']['matchConstraints']['namespaceSelector'] = {'matchLabels': {'bypass': 'true'}}
        self.assertNotEqual(installation.normalize(actual), installation.normalize(expected))

if __name__ == '__main__':
    unittest.main()
