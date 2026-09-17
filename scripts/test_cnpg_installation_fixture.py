"""Check bounded installation resources and cleanup after partial failure."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import traceback
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

    def test_failed_read_retries_before_confirming_absence(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            failed = subprocess.CompletedProcess(['oc'], 1, '', 'private-error-value')
            absent = subprocess.CompletedProcess(['oc'], 0, '', '')
            with patch.object(fixture.subprocess, 'run', side_effect=[failed, absent]) as calls, patch.object(fixture.time, 'sleep'):
                self.assertIsNone(installation.get('Namespace', 'stego-cnpg-database-ci'))
                self.assertEqual(calls.call_count, 2)
                self.assertEqual(calls.call_args.kwargs['timeout'], 30)

    def test_invalid_read_never_means_absence(self):
        for output in ['[]', 'null', '{}', '{"items":null}', 'private-invalid-json']:
            with self.subTest(output=output), tempfile.TemporaryDirectory() as directory:
                installation = fixture.Installation(self.args(directory))
                reply = subprocess.CompletedProcess(['oc'], 0, output, '')
                with patch.object(fixture.subprocess, 'run', return_value=reply) as calls, patch.object(fixture.time, 'sleep'):
                    with self.assertRaisesRegex(RuntimeError, 'three attempts'):
                        installation.get('Namespace', 'stego-cnpg-database-ci')
                    self.assertEqual(calls.call_count, 3)

    def test_read_timeout_is_bounded_and_private(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            failure = subprocess.TimeoutExpired(['oc', 'private-command-value'], 30, output='private-output-value')
            with patch.object(fixture.subprocess, 'run', side_effect=failure) as calls, patch.object(fixture.time, 'sleep'):
                try:
                    installation.get('Namespace', 'stego-cnpg-database-ci')
                except RuntimeError:
                    self.assertNotIn('private-', traceback.format_exc())
                else:
                    self.fail('Repeated read timeout was accepted')
                self.assertEqual(calls.call_count, 3)

    def test_mutation_failure_is_not_replayed(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            failed = subprocess.CompletedProcess(['oc'], 1, '', 'private-error-value')
            with patch.object(fixture.subprocess, 'run', return_value=failed) as calls, patch.object(fixture.time, 'sleep') as sleep:
                with self.assertRaises(RuntimeError) as error:
                    installation.oc('delete', 'Namespace', 'stego-cnpg-database-ci')
                self.assertNotIn('private-', str(error.exception))
                self.assertEqual(calls.call_count, 1)
                sleep.assert_not_called()

    def test_valid_reads_preserve_resource_identity_and_empty_lists(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            for value in [{'metadata': {'uid': 'original'}}, {'items': []}]:
                reply = subprocess.CompletedProcess(['oc'], 0, json.dumps(value), '')
                with patch.object(fixture.subprocess, 'run', return_value=reply) as call, patch.object(fixture.time, 'sleep') as sleep:
                    self.assertEqual(installation.oc('get', 'pods', '-o', 'json'), value)
                    self.assertIn('--context=operator-test-context', call.call_args.args[0])
                    self.assertEqual(call.call_count, 1)
                    sleep.assert_not_called()

    def test_empty_list_reply_requires_valid_json(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            reply = subprocess.CompletedProcess(['oc'], 0, '', '')
            with patch.object(fixture.subprocess, 'run', return_value=reply) as calls, patch.object(fixture.time, 'sleep'):
                with self.assertRaisesRegex(RuntimeError, 'three attempts'):
                    installation.oc('get', 'pods', '-o', 'json')
                self.assertEqual(calls.call_count, 3)

    def test_operation_cannot_be_hidden_after_flags(self):
        with tempfile.TemporaryDirectory() as directory:
            installation = fixture.Installation(self.args(directory))
            with patch.object(fixture.subprocess, 'run') as call:
                with self.assertRaisesRegex(ValueError, 'operation first'):
                    installation.oc('-n', 'stego-service-ci', 'get', 'pods', '-o', 'json')
                call.assert_not_called()

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
        probe = policy['ingress'][-1]['from'][-2]
        self.assertEqual(probe['namespaceSelector'], gateway['namespaceSelector'])
        self.assertEqual(probe['podSelector'], {'matchExpressions': [{'key': 'stego.test/network-probe', 'operator': 'Exists'}]})
        self.assertEqual(policy['ingress'][-1]['ports'], [{'port': 5432, 'protocol': 'TCP'}])

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

    def test_console_database_peer_requires_its_allocated_gateway_namespace(self):
        items = fixture.definitions('stego-service-ci', 'stego-cnpg-database-ci', 'gp3-csi', [('192.0.2.1', 6443)])
        policy = next(o['spec'] for o in items if o['kind'] == 'NetworkPolicy')
        selector = {'matchLabels': {'app.kubernetes.io/name': 'hypershell-gateway-console'}}
        peers = [(rule, peer) for rule in policy['ingress'] for peer in rule['from'] if peer.get('podSelector') == selector]
        self.assertEqual(len(peers), 1)
        rule, peer = peers[0]
        marker = hashlib.sha256(b'stego-service-ci.hypershell-namespace-allocation').hexdigest()[:32]
        self.assertEqual(peer['namespaceSelector'], {'matchLabels': {
            'stego.dev/allocator': marker, 'stego.dev/allocation-profile': 'gateway'}})
        self.assertEqual(rule['ports'], [{'port': 5432, 'protocol': 'TCP'}])
        # The console deliberately lacks the Gateway Service selector label.
        self.assertNotIn('matchExpressions', peer['podSelector'])

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
