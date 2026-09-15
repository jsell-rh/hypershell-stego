"""Check the unrelated listener's scope, limits, and cleanup identity."""
import json
import tempfile
import unittest
from unittest.mock import Mock

from network_peer_fixture import definitions, listener_addresses, peer_namespace, require_owner, Fixture

CONTROL = 'stego-service-20260915-123abc'


class NetworkPeerFixture(unittest.TestCase):
    def test_address_listeners_are_separate_and_bounded(self):
        objects = definitions(CONTROL, 'nonce', endpoint_change=True)
        pods = [o for o in objects if o['kind'] == 'Pod']
        self.assertEqual([p['metadata']['name'] for p in pods], ['peer', 'address-a', 'address-b'])
        selector = next(o for o in objects if o['kind'] == 'Service')['spec']['selector']
        self.assertEqual([p['metadata']['name'] for p in pods if p['metadata']['labels']['app'] == selector['app']], ['peer'])
        quota = next(o for o in objects if o['kind'] == 'ResourceQuota')['spec']['hard']
        self.assertEqual(quota, {'pods': '3', 'services': '1', 'limits.cpu': '300m',
            'limits.memory': '384Mi', 'limits.ephemeral-storage': '48Mi', 'persistentvolumeclaims': '0'})
        for pod in pods:
            self.assertFalse(pod['spec']['automountServiceAccountToken'])
            self.assertNotIn('volumes', pod['spec'])
            self.assertEqual(pod['spec']['activeDeadlineSeconds'], 1800)
            self.assertEqual(pod['spec']['containers'][0]['resources'], pods[0]['spec']['containers'][0]['resources'])
        with self.assertRaises(ValueError):
            definitions(CONTROL, 'nonce', endpoint_change='true')

    def test_address_receipt_requires_distinct_valid_ips_and_fixed_uids(self):
        def pod(uid, ip):
            return {'metadata': {'uid': uid}, 'status': {'podIP': ip}}
        ids = {'address-a': 'a', 'address-b': 'b'}
        valid = {'address-a': pod('a', '10.128.0.1'), 'address-b': pod('b', '10.128.0.2')}
        self.assertEqual(listener_addresses(valid, ids), {'address-a': {'uid': 'a', 'address': '10.128.0.1:8080'},
                                                       'address-b': {'uid': 'b', 'address': '10.128.0.2:8080'}})
        for address in ['', '10.128.0.1', '127.0.0.1', '169.254.1.2', '224.0.0.1', '0.0.0.0', '255.255.255.255', '::ffff:10.128.0.2', '2001:db8::1%eth0']:
            with self.subTest(address=address), self.assertRaises(ValueError):
                listener_addresses(dict(valid, **{'address-b': pod('b', address)}), ids)
        with self.assertRaisesRegex(RuntimeError, 'replaced'):
            listener_addresses(dict(valid, **{'address-b': pod('replacement', '10.128.0.2')}), ids)

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
