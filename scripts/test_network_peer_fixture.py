"""Check the unrelated listener's scope, limits, and cleanup identity."""
import json
import tempfile
import unittest
from unittest.mock import Mock

from network_peer_fixture import definitions, peer_namespace, require_owner, Fixture

CONTROL = 'stego-service-20260915-123abc'


class NetworkPeerFixture(unittest.TestCase):
    def test_listener_has_no_ingress_policy_token_or_secret(self):
        objects = definitions(CONTROL, 'nonce')
        self.assertEqual([o['kind'] for o in objects], ['Namespace', 'ResourceQuota', 'NetworkPolicy', 'ServiceAccount', 'Service', 'Pod'])
        policy = objects[2]['spec']
        self.assertEqual(policy, {'podSelector': {}, 'policyTypes': ['Egress'], 'egress': []})
        pod = objects[-1]
        self.assertFalse(objects[3]['automountServiceAccountToken'])
        self.assertEqual(objects[4]['spec']['selector'], {'app': pod['metadata']['labels']['app']})
        for obj in objects:
            self.assertFalse(any(key.startswith('stego.dev/') or key.startswith('hypershell.') for key in obj['metadata']['labels']))
        spec = pod['spec']
        self.assertFalse(spec['automountServiceAccountToken'])
        self.assertNotIn('volumes', spec)
        self.assertEqual(spec['activeDeadlineSeconds'], 1800)
        self.assertEqual(spec['restartPolicy'], 'Never')
        container = spec['containers'][0]
        self.assertIn('@sha256:', container['image'])
        self.assertTrue(container['securityContext']['readOnlyRootFilesystem'])
        self.assertFalse(container['securityContext']['allowPrivilegeEscalation'])
        self.assertEqual(container['securityContext']['capabilities'], {'drop': ['ALL']})
        self.assertEqual(container['resources']['limits'], {'cpu': '100m', 'memory': '128Mi', 'ephemeral-storage': '16Mi'})

    def test_names_are_limited_to_disposable_direct_fixtures(self):
        self.assertEqual(peer_namespace(CONTROL), CONTROL + '-peer')
        for value in ['default', 'stego-service-ci', CONTROL + '/pods', CONTROL.upper(), CONTROL + '-extra']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                peer_namespace(value)
        with self.assertRaises(ValueError):
            Fixture('', CONTROL, '/unused')

    def record(self):
        return {'control': CONTROL, 'namespace': peer_namespace(CONTROL), 'nonce': 'expected',
                'namespace_uid': 'original', 'lease_uid': 'lease'}

    def namespace(self):
        return {'metadata': {'name': peer_namespace(CONTROL), 'uid': 'original', 'resourceVersion': '123',
                             'labels': {'stego.test/browser-run': CONTROL, 'stego.test/peer-run': 'expected'}}}

    def test_owner_or_identity_changes_prevent_cleanup(self):
        for change in [lambda v: v['metadata'].update(uid='replacement'),
                       lambda v: v['metadata']['labels'].update({'stego.test/peer-run': 'other'}),
                       lambda v: v['metadata']['labels'].update({'stego.test/browser-run': 'other'})]:
            with tempfile.TemporaryDirectory() as directory:
                fixture = Fixture('explicit-context', CONTROL, directory)
                fixture.save(self.record())
                fixture.lease = Mock(return_value='lease')
                value = self.namespace()
                change(value)
                fixture.request = Mock(return_value=value)
                with self.assertRaisesRegex(RuntimeError, 'owner or identity'):
                    fixture.cleanup()
                self.assertEqual(fixture.request.call_count, 1)

    def test_partial_creation_requires_the_exact_nonce(self):
        record = self.record()
        del record['namespace_uid']
        require_owner(self.namespace(), record)
        foreign = self.namespace()
        foreign['metadata']['labels']['stego.test/peer-run'] = 'other'
        with self.assertRaises(RuntimeError):
            require_owner(foreign, record)

    def test_cleanup_uses_uid_and_revision_and_observes_absence(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = Fixture('explicit-context', CONTROL, directory)
            fixture.save(self.record())
            fixture.lease = Mock(return_value='lease')
            fixture.request = Mock(side_effect=[self.namespace(), {}, None])
            fixture.cleanup()
            deletion = fixture.request.call_args_list[1]
            self.assertEqual(deletion.args, ('delete', '--raw=/api/v1/namespaces/' + peer_namespace(CONTROL), '-f', '-'))
            self.assertEqual(deletion.kwargs['body']['preconditions'], {'uid': 'original', 'resourceVersion': '123'})
            self.assertTrue(json.loads(fixture.path.read_text())['namespace_absent'])

    def test_replaced_lease_prevents_namespace_requests(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = Fixture('explicit-context', CONTROL, directory)
            fixture.save(self.record())
            fixture.lease = Mock(return_value='other')
            fixture.request = Mock()
            with self.assertRaisesRegex(RuntimeError, 'Lease identity'):
                fixture.cleanup()
            fixture.request.assert_not_called()


if __name__ == '__main__':
    unittest.main()
