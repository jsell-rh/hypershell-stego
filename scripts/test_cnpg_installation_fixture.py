"""Check bounded installation resources and cleanup after partial failure."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('cnpg_fixture', Path(__file__).with_name('cnpg-installation-fixture.py'))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class InstallationTests(unittest.TestCase):
    def args(self, directory):
        return SimpleNamespace(context='operator-test-context', namespace='stego-service-ci', database_namespace='stego-cnpg-database-ci',
                               storage_class='gp3-csi', results=Path(directory), lease_holder='stego-cnpg-live-0123456789abcdef', lease_uid='lease-uid')

    def test_server_scope_limits_and_tls(self):
        items = fixture.definitions('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', [('192.0.2.1', 6443), ('192.0.2.2', 443)])
        self.assertFalse(any(o['kind'] in ['ClusterRole', 'ClusterRoleBinding'] for o in items))
        cluster = next(o for o in items if o['kind'] == 'Cluster')
        self.assertEqual(cluster['spec']['instances'], 2)
        self.assertIn('@sha256:', cluster['spec']['imageName'])
        self.assertEqual(cluster['spec']['postgresql']['pg_hba'], ['hostnossl all all all reject', 'hostssl all all all scram-sha-256'])
        self.assertEqual(cluster['spec']['resources']['limits']['memory'], '768Mi')
        self.assertEqual(cluster['spec']['storage'], {'size': '1Gi', 'storageClass': 'gp3-csi'})
        observer = next(o for o in items if o['kind'] == 'Role')
        self.assertEqual({verb for rule in observer['rules'] for verb in rule['verbs']}, {'get', 'delete'})
        self.assertFalse(any('secrets' in rule['resources'] or 'clusters' in rule['resources'] and 'delete' in rule['verbs'] for rule in observer['rules']))
        binding = next(o for o in items if o['kind'] == 'RoleBinding' and o['metadata']['name'] == 'fixture-observer')
        self.assertEqual(binding['subjects'], [{'kind': 'ServiceAccount', 'name': 'service-check', 'namespace': 'stego-service-ci'}])
        job = next(o for o in items if o['kind'] == 'Job')
        self.assertEqual(job['spec']['activeDeadlineSeconds'], 1500)
        pod = job['spec']['template']['spec']
        self.assertFalse(pod['automountServiceAccountToken'])
        self.assertTrue(pod['securityContext']['runAsNonRoot'])
        self.assertFalse(pod['containers'][0]['securityContext']['allowPrivilegeEscalation'])

    def test_network_access_has_no_wildcard_destination(self):
        items = fixture.definitions('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', [('192.0.2.1', 6443)])
        policy = next(o['spec'] for o in items if o['kind'] == 'NetworkPolicy')
        for direction, field in [('ingress', 'from'), ('egress', 'to')]:
            for rule in policy[direction]:
                self.assertTrue(rule[field])
                self.assertTrue(rule['ports'])
                for peer in rule[field]:
                    if 'ipBlock' in peer:
                        self.assertEqual(peer['ipBlock']['cidr'], '192.0.2.1/32')
                    else:
                        self.assertTrue(peer['namespaceSelector']['matchLabels'])
        gateway = policy['ingress'][-1]['from'][-1]
        self.assertEqual(gateway['namespaceSelector']['matchLabels']['stego.dev/allocation-profile'], 'gateway')
        self.assertTrue(gateway['podSelector']['matchExpressions'])

    def test_invalid_placement_and_endpoint_inputs_fail(self):
        for namespace, database, storage, endpoints in [
            ('default', 'stego-cnpg-database-ci', 'gp3-csi', [('192.0.2.1', 443)]),
            ('stego-service-ci', 'openshell-db-old', 'gp3-csi', [('192.0.2.1', 443)]),
            ('stego-service-ci', 'stego-cnpg-database-ci', '../storage', [('192.0.2.1', 443)]),
            ('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', []),
            ('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', [('0.0.0.0', 443)]),
            ('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', [('192.0.2.1', 0)]),
        ]:
            with self.subTest(database=database, endpoints=endpoints), self.assertRaises(ValueError):
                fixture.definitions(namespace, database, storage, endpoints)

    def test_empty_or_different_lease_cannot_install(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            installation.get = Mock(return_value={'metadata': {'uid': 'lease-uid'}, 'spec': {'holderIdentity': 'another-run'}})
            with self.assertRaisesRegex(RuntimeError, 'held live-test Lease'):
                installation.require_lease()
            installation.args.lease_holder = ''
            with self.assertRaisesRegex(RuntimeError, 'distinct CNPG run'):
                installation.require_lease()

    def test_lost_create_response_records_observed_owner_without_secret(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            journal = []
            item = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'cnpg-credentials', 'namespace': 'stego-service-ci', 'labels': {fixture.LABEL: 'stego-service-ci'}},
                    'stringData': {'fixture.json': 'private-test-value'}}
            installation.oc = Mock(side_effect=subprocess.TimeoutExpired('oc', 30))
            observed = copy.deepcopy(item)
            observed['metadata'].update(uid='created-uid', annotations={'stego.test/cnpg-holder': installation.args.lease_holder})
            installation.get = Mock(return_value=observed)
            with self.assertRaises(subprocess.TimeoutExpired):
                installation.create(item, journal)
            saved = installation.journal.read_text()
            self.assertNotIn('private-test-value', saved)
            self.assertNotIn('stringData', saved)
            self.assertEqual(json.loads(saved)[0]['metadata']['uid'], 'created-uid')
            self.assertEqual(json.loads(saved)[0]['state'], 'observed_after_error')

    def test_application_job_and_changed_owner_prevent_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            installation.require_lease = Mock()
            installation.oc = Mock()
            installation.get = Mock(return_value={'metadata': {'uid': 'running-job'}})
            with self.assertRaisesRegex(RuntimeError, 'application Job still exists'):
                installation.remove()
            installation.oc.assert_not_called()
            record = {'kind': 'Namespace', 'metadata': {'name': 'stego-cnpg-database-ci', 'uid': 'original'}}
            fixture.write(installation.journal, [record])
            installation.get = Mock(side_effect=[None, {'metadata': {'uid': 'replacement', 'labels': {fixture.LABEL: 'stego-service-ci'}}}])
            with self.assertRaisesRegex(RuntimeError, 'different resource owner'):
                installation.remove()
            installation.oc.assert_not_called()


if __name__ == '__main__':
    unittest.main()
