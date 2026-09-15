"""Check the CNPG test operator's installation and ownership boundaries."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('cnpg_operator', Path(__file__).with_name('cnpg-test-operator.py'))
operator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(operator)


class InstallationBoundary(unittest.TestCase):
    def test_names_are_explicit_and_separate_from_gateway_allocation(self):
        operator.validate_names('prepare', 'stego-service-ci', 'stego-cnpg-database-ci')
        for database in [None, '', 'default', 'openshell-db-0123456789abcdef',
                         'stego-cnpg-database-', 'stego-cnpg-database-A',
                         'stego-cnpg-database-a/b', 'stego-cnpg-database-' + 'x' * 60]:
            with self.subTest(database=database), self.assertRaises(ValueError):
                operator.validate_names('prepare', 'stego-service-ci', database)
        for namespace in ['', 'default', 'stego-service-', 'stego-service-../other',
                          'stego-service-' + 'x' * 60]:
            with self.subTest(namespace=namespace), self.assertRaises(ValueError):
                operator.validate_names('prepare', namespace, 'stego-cnpg-database-ci')
        for action in ['install', 'remove']:
            operator.validate_names(action, 'stego-service-ci', None)
            with self.assertRaises(ValueError):
                operator.validate_names(action, 'stego-service-ci', 'stego-cnpg-database-ci')

    def fixture(self):
        # Include global and local rules in one entry to test rule separation.
        return [
            {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'cnpg-system'}},
            {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
             'metadata': {'name': 'cnpg-manager'}, 'rules': [
                 {'apiGroups': [''], 'resources': ['nodes', 'pods', 'secrets'], 'verbs': ['get', 'list']},
                 {'apiGroups': ['admissionregistration.k8s.io'],
                  'resources': ['mutatingwebhookconfigurations', 'validatingwebhookconfigurations'], 'verbs': ['get', 'update']}]},
            {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRoleBinding',
             'metadata': {'name': 'discard-global-manager-binding'}},
            {'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingWebhookConfiguration',
             'metadata': {'name': 'cnpg-validating-webhook-configuration'},
             'webhooks': [{'name': 'cluster.cnpg.io', 'namespaceSelector': {}, 'timeoutSeconds': 30}]},
            {'apiVersion': 'apps/v1', 'kind': 'Deployment',
             'metadata': {'name': 'cnpg-controller-manager', 'namespace': 'cnpg-system'},
             'spec': {'replicas': 3, 'template': {'spec': {'containers': [{
                 'name': 'manager', 'image': 'upstream-tag', 'args': ['--max-concurrent-reconciles=10'],
                 'env': [{'name': 'OPERATOR_IMAGE_NAME', 'value': 'upstream-tag'}],
                 'startupProbe': {'failureThreshold': 10},
                 'securityContext': {'runAsUser': 10001, 'runAsGroup': 10001, 'allowPrivilegeEscalation': False}}],
                 'volumes': [{'name': 'scratch', 'emptyDir': {}}]}}}},
        ]

    def prepare(self, directory, existing=False):
        raw = '\n---\n'.join(json.dumps(o) for o in self.fixture()).encode()
        args = SimpleNamespace(namespace='stego-service-ci', database_namespace='stego-cnpg-database-ci',
                               context='explicit-test-context', evidence=Path(directory))
        with patch.object(operator.urllib.request, 'urlopen', return_value=io.BytesIO(raw)), \
             patch.object(operator, 'SHA256', hashlib.sha256(raw).hexdigest()), \
             patch.object(operator, 'manifest_documents', return_value=self.fixture()), \
             patch.object(operator, 'get', return_value={'existing': True} if existing else None):
            operator.prepare(args)
        return json.loads((args.evidence / 'cnpg-plan.json').read_text())

    def test_plan_confines_watch_and_webhooks_and_keeps_deadline(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = self.prepare(directory)
            self.assertNotIn('database_id', plan)
            self.assertEqual(plan['database_namespace'], 'stego-cnpg-database-ci')
            items = plan['items']
            self.assertFalse(any(o['kind'] == 'Cluster' for o in items))
            self.assertEqual([o['metadata']['name'] for o in items if o['kind'] == 'Namespace'], ['cnpg-system'])
            role = next(o for o in items if o['kind'] == 'ClusterRole' and o['metadata']['name'] == 'cnpg-manager')
            self.assertEqual(role['rules'][0]['resources'], ['pods', 'secrets'])
            bindings = [o for o in items if o['kind'] == 'ClusterRoleBinding']
            self.assertEqual([o['metadata']['name'] for o in bindings], ['cnpg-test-observer'])
            observer = next(o for o in items if o['kind'] == 'ClusterRole' and o['metadata']['name'] == 'cnpg-test-observer')
            self.assertEqual(observer['rules'][0]['resources'], ['nodes'])
            self.assertEqual(observer['rules'][1]['resourceNames'], ['cnpg-mutating-webhook-configuration', 'cnpg-validating-webhook-configuration'])
            webhook = next(o for o in items if o['kind'] == 'ValidatingWebhookConfiguration')['webhooks'][0]
            self.assertEqual(webhook['namespaceSelector'], {'matchLabels': {'kubernetes.io/metadata.name': plan['database_namespace']}})
            self.assertEqual(webhook['timeoutSeconds'], 5)
            deployment = next(o for o in items if o['kind'] == 'Deployment')
            self.assertEqual(deployment['spec']['replicas'], 0)
            manager = deployment['spec']['template']['spec']['containers'][0]
            self.assertEqual(manager['image'], operator.IMAGE)
            self.assertIn({'name': 'WATCH_NAMESPACE', 'value': plan['database_namespace']}, manager['env'])
            self.assertEqual(manager['resources']['limits']['cpu'], '500m')
            self.assertEqual(manager['resources']['limits']['memory'], '512Mi')
            lifetime = next(o for o in items if o['kind'] == 'Job')
            self.assertEqual(lifetime['spec']['activeDeadlineSeconds'], 1800)
            self.assertTrue(lifetime['spec']['suspend'])
            self.assertEqual(lifetime['spec']['backoffLimit'], 0)
            self.assertFalse(lifetime['spec']['template']['spec']['automountServiceAccountToken'])

    def test_existing_resources_and_journals_are_not_replaced(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(RuntimeError, 'existing resource'):
                self.prepare(directory, existing=True)
            self.assertFalse((Path(directory) / 'cnpg-plan.json').exists())
            self.prepare(directory)
            before = (Path(directory) / 'cnpg-plan.json').read_bytes()
            with self.assertRaisesRegex(RuntimeError, 'ownership journal'):
                self.prepare(directory)
            self.assertEqual((Path(directory) / 'cnpg-plan.json').read_bytes(), before)

    def test_cleanup_requires_database_namespace_absent(self):
        with tempfile.TemporaryDirectory() as directory:
            self.prepare(directory)
            args = SimpleNamespace(context='explicit-test-context', evidence=Path(directory))
            with patch.object(operator, 'get', return_value={'metadata': {'name': 'stego-cnpg-database-ci'}}), \
                 patch.object(operator, 'oc') as oc:
                with self.assertRaisesRegex(RuntimeError, 'keep CNPG'):
                    operator.remove(args)
                oc.assert_not_called()


if __name__ == '__main__':
    unittest.main()
