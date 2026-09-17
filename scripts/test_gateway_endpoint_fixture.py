"""Check endpoint-test ownership and exact admission changes without a cluster."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

import gateway_endpoint_fixture as fixture


class EndpointBoundary(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'acceptance').mkdir()
        self.source = self.root / 'acceptance/browser-inspection-source.json'
        self.source.write_text(json.dumps({'network_endpoint_change': {'endpoint': 'network-probe'}}))
        self.control = 'stego-service-20260915-abcdef'
        self.record = {'control': self.control, 'namespace': self.control + '-peer', 'namespace_uid': 'peer-uid',
            'phase': 'ready', 'endpoint_change': True, 'ingress_policy': False, 'nonce': 'a' * 32,
            'pods': {'address-a': 'uid-a', 'address-b': 'uid-b'}, 'address_listeners': {
                'address-a': {'uid': 'uid-a', 'address': '192.0.2.10:8080'},
                'address-b': {'uid': 'uid-b', 'address': '192.0.2.11:8080'}}}

    def read(self, record):
        (self.root / 'network-peer.json').write_text(json.dumps(record))
        return fixture.inputs(self.root, self.root, self.control)

    def test_source_manifest_has_a_separate_bounded_size(self):
        record = {'network_endpoint_change': {'endpoint': 'network-probe'},
                  'source_sha256': {'file': 'a' * fixture.FIXTURE_RECORD_LIMIT}}
        self.source.write_text(json.dumps(record))
        self.assertTrue(fixture.enabled(self.root))
        with self.assertRaises(ValueError):
            fixture.read_json(self.source)
        self.source.write_text(json.dumps({'padding': 'a' * fixture.INSPECTION_RECORD_LIMIT}))
        with self.assertRaises(ValueError):
            fixture.enabled(self.root)

    def test_two_owned_listeners_supply_the_fixed_change(self):
        value = self.read(self.record)
        self.assertEqual(value['initial'], '192.0.2.10:8080')
        self.assertEqual(value['replacement'], '192.0.2.11:8080')
        self.assertEqual(value['nonce'], 'a' * 32)

    def test_incomplete_foreign_or_replaced_listeners_are_rejected(self):
        changes = [lambda r: r.update(control='other'), lambda r: r.update(namespace='default'),
            lambda r: r.update(phase='creating'), lambda r: r.update(endpoint_change=1),
            lambda r: r.update(ingress_policy=True), lambda r: r.update(nonce=''),
            lambda r: r['address_listeners']['address-a'].update(uid='replaced'),
            lambda r: r['address_listeners']['address-b'].update(address='192.0.2.10:8080'),
            lambda r: r['address_listeners'].pop('address-b')]
        for change in changes:
            record = copy.deepcopy(self.record)
            change(record)
            with self.assertRaises(ValueError):
                self.read(record)

    def test_addresses_are_canonical_unicast_and_use_one_port(self):
        self.assertEqual(fixture.address('[2001:db8::1]:8080'), {'cidr': '2001:db8::1/128', 'port': '8080'})
        for value in ['127.0.0.1:8080', '192.0.2.1:443', 'database.test:8080', '[::]:8080',
                      '255.255.255.255:8080', '[::ffff:192.0.2.1]:8080', '224.0.0.1:8080',
                      '[fe80::1%eth0]:8080', '2001:db8::1:8080', None]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                fixture.address(value)

    def test_mode_requires_the_exact_declaration_and_rejects_retired_cnpg(self):
        for record in [{'network_endpoint_change': {'endpoint': 'kubernetes'}},
                       {'network_endpoint_change': True},
                       {'network_endpoint_change': {'endpoint': 'network-probe'}, 'cnpg_installation': {'cluster': 'gateway-database'}}]:
            self.source.write_text(json.dumps(record))
            with self.assertRaises(ValueError):
                fixture.enabled(self.root)
        self.source.write_text('{}')
        self.assertIsNone(fixture.inputs(self.root, self.root, self.control))

    def policies(self):
        before = {'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingAdmissionPolicy',
            'metadata': {'name': 'fixture.allocation'}, 'spec': {'failurePolicy': 'Fail',
            'validations': [{'expression': 'KEEP_OWNERSHIP'}], 'variables': [{'name': 'networkEndpoints',
            'expression': json.dumps({'gateway': [{'cidr': '192.0.2.1/32', 'port': '443'}, fixture.address('192.0.2.10:8080')]})}]}}
        after = copy.deepcopy(before)
        after['spec']['variables'][0]['expression'] = before['spec']['variables'][0]['expression'].replace('192.0.2.10/32', '192.0.2.11/32')
        return before, after

    def test_only_the_fixed_address_can_change(self):
        before, after = self.policies()
        fixture.verify_policy_change(before, after, '192.0.2.10:8080', '192.0.2.11:8080')
        changes = [lambda p: p['metadata'].update(name='foreign'),
            lambda p: p['spec'].update(failurePolicy='Ignore'),
            lambda p: p['spec']['validations'][0].update(expression='true'),
            lambda p: p['spec']['variables'].append(copy.deepcopy(p['spec']['variables'][0])),
            lambda p: p['spec']['variables'][0].update(expression=p['spec']['variables'][0]['expression'].replace('192.0.2.1/32', '0.0.0.0/0')),
            lambda p: p['spec']['variables'][0].update(expression=p['spec']['variables'][0]['expression'].replace('gateway', 'sandbox'))]
        for change in changes:
            modified = copy.deepcopy(after)
            change(modified)
            with self.assertRaises(ValueError):
                fixture.verify_policy_change(before, modified, '192.0.2.10:8080', '192.0.2.11:8080')
        with self.assertRaises(ValueError):
            fixture.verify_policy_change(before, before, '192.0.2.10:8080', '192.0.2.11:8080')

    def test_full_render_cannot_add_permissions_or_hide_duplicate_objects(self):
        before, after = self.policies()
        role = {'kind': 'ClusterRole', 'metadata': {'name': 'fixture.worker'}, 'rules': []}
        old = {'apiVersion': 'v1', 'kind': 'List', 'items': [before, role]}
        new = {'apiVersion': 'v1', 'kind': 'List', 'items': [after, copy.deepcopy(role)]}
        self.assertEqual(fixture.policy_change(old, new, '192.0.2.10:8080', '192.0.2.11:8080'), (before, after))
        for change in [lambda d: d['items'].pop(),
                       lambda d: d['items'].append(copy.deepcopy(role)),
                       lambda d: d['items'][1]['rules'].append({'verbs': ['*']})]:
            modified = copy.deepcopy(new)
            change(modified)
            with self.assertRaises(ValueError):
                fixture.policy_change(old, modified, '192.0.2.10:8080', '192.0.2.11:8080')


if __name__ == '__main__':
    unittest.main()
