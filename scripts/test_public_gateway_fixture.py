"""Check explicit public test inputs without a cluster connection."""
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from public_gateway_fixture import apply_public_fixture, read_config
from internal_gateway_fixture import apply_internal_fixture


class PublicGatewayFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
                        '-keyout', str(cls.root / 'key.pem'), '-out', str(cls.root / 'ca.pem'), '-days', '1',
                        '-subj', '/CN=public-fixture-test', '-addext', 'basicConstraints=critical,CA:TRUE'],
                       check=True, capture_output=True, timeout=10)
        cls.config = {'domain': 'apps.example.test', 'issuer': 'test-ca', 'router': 'default',
                      'ca_pem': (cls.root / 'ca.pem').read_text(), 'endpoints': ['192.0.2.3:443', '[2001:db8::3]:443']}

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def write(self, config):
        path = self.root / 'config.json'
        path.write_text(json.dumps(config))
        return str(path)

    def test_explicit_inputs(self):
        self.assertEqual(read_config(self.write(self.config)), self.config)
        cfg = copy.deepcopy(self.config)
        cfg['endpoints'].reverse()
        self.assertEqual(read_config(self.write(cfg)), self.config)

    def test_rejected_inputs(self):
        cases = [lambda c: c.update(extra=True), lambda c: c.update(domain='*.example.test'),
                 lambda c: c.update(domain='127.0.0.1'), lambda c: c.update(router='*'),
                 lambda c: c.update(issuer='../issuer'), lambda c: c.update(ca_pem='invalid'),
                 lambda c: c.update(ca_pem=c['ca_pem']+(self.root/'key.pem').read_text()),
                 lambda c: c.update(ca_pem=c['ca_pem']+'extra text'), lambda c: c.update(endpoints=[]),
                 lambda c: c.update(endpoints=c['endpoints']*9), lambda c: c.update(endpoints=[True])]
        for value in ['127.0.0.1:443', '169.254.169.254:443', '255.255.255.255:443', '224.0.0.1:443',
                      '0.0.0.0:443', '[::1]:443', '[::]:443', '[::ffff:192.0.2.3]:443',
                      '[192.0.2.3]:443', '[2001:db8::3%eth0]:443', '192.0.2.3:80', '192.0.2.0/24:443', 'host.example.test:443']:
            cases.append(lambda c, value=value: c.update(endpoints=[value]))
        cases.append(lambda c: c.update(endpoints=['[2001:db8::3]:443', '[2001:0db8::3]:443']))
        for change in cases:
            cfg = copy.deepcopy(self.config); change(cfg)
            with self.subTest(change=change), self.assertRaises(ValueError):
                read_config(self.write(cfg))
        path = self.root / 'invalid.json'
        for text in ['{"domain":"a","domain":"b"}', ' '*(65537), '{} {}']:
            path.write_text(text)
            with self.assertRaises(ValueError):
                read_config(path)

    def test_complete_fixture_retains_separate_trust_and_limits(self):
        root = self.root
        config_path = self.write(self.config)
        (root / 'kubernetes-endpoints.json').write_text(json.dumps({'items': [{'endpoints': [{'conditions': {'ready': True}, 'addresses': ['192.0.2.1']}], 'ports': [{'protocol': 'TCP', 'name': 'https', 'port': 443}]}]}))
        (root / 'kubernetes-service.json').write_text(json.dumps({'spec': {'clusterIP': '10.0.0.1', 'clusterIPs': ['10.0.0.1']}}))
        for name in ['ca.crt', 'server.crt']:
            (root / name).write_bytes((root / 'ca.pem').read_bytes())
        (root / 'server.key').write_bytes((root / 'key.pem').read_bytes())
        script = Path(__file__).parent / 'render-service-fixture.py'
        environment = dict(os.environ, STEGO_TEST_GATEWAY_PUBLIC_CONFIG=config_path,
                           STEGO_TEST_GATEWAY_INTERNAL_CA_FILE=str(root / 'ca.pem'),
                           STEGO_TEST_REQUIRE_PUBLIC_GATEWAY='1', STEGO_TEST_CNPG_FIXTURE='0')
        subprocess.run([sys.executable, str(script), 'stego-service-ci', str(root), '1', '1', 'test-ca'],
                       check=True, capture_output=True, timeout=5, env=environment)
        document = json.loads((root / 'job.json').read_text())
        config = next(item for item in document['items'] if item['kind'] == 'ConfigMap' and item['metadata']['name'] == 'database-ca')
        self.assertEqual(set(config['data']), {'server.crt', 'gateway-public.json', 'gateway-internal-ca.pem'})
        self.assertEqual(config['data']['gateway-internal-ca.pem'], (root / 'ca.pem').read_text())
        self.assertEqual(json.loads(config['data']['gateway-public.json']), self.config)
        self.assertEqual(config['metadata']['labels']['stego.test/browser-run'], 'stego-service-ci')
        policy = next(item['spec'] for item in document['items'] if item['kind'] == 'NetworkPolicy' and item['metadata']['name'] == 'fixture-ingress')
        gateway_rules = [rule for rule in policy['ingress'] if any('namespaceSelector' in peer for peer in rule['from'])]
        marker = hashlib.sha256(b'stego-service-ci.hypershell-namespace-allocation').hexdigest()[:32]
        self.assertEqual(gateway_rules, [{'from': [{'namespaceSelector': {'matchLabels': {
            'stego.dev/allocator': marker, 'stego.dev/allocation-profile': 'gateway'}}}],
            'ports': [{'port': 5432, 'protocol': 'TCP'}, {'port': 19093, 'protocol': 'TCP'}]}])
        job = next(item for item in document['items'] if item['kind'] == 'Job')
        self.assertLessEqual(job['spec']['activeDeadlineSeconds'], 1800)
        self.assertEqual(job['spec']['backoffLimit'], 0)
        for container in job['spec']['template']['spec']['containers']:
            self.assertFalse(container['securityContext']['allowPrivilegeEscalation'])
            self.assertIn('cpu', container['resources']['limits'])
            self.assertIn('memory', container['resources']['limits'])
        settings = {row['name']: row.get('value') for row in job['spec']['template']['spec']['containers'][0]['env']}
        self.assertNotIn('STEGO_TEST_UNRELATED_NETWORK_HOST', settings)
        subprocess.run([sys.executable, str(script), 'stego-service-20260915-123abc', str(root), '1', '1', 'test-ca'],
                       check=True, capture_output=True, timeout=5, env=environment)
        document = json.loads((root / 'job.json').read_text())
        job = next(item for item in document['items'] if item['kind'] == 'Job')
        settings = {row['name']: row.get('value') for row in job['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(settings['STEGO_TEST_UNRELATED_NETWORK_HOST'],
                         'peer.stego-service-20260915-123abc-peer.svc.cluster.local')

    def test_internal_trust_rejects_invalid_input_before_changes(self):
        document = {'items': []}
        path = self.root / 'internal-invalid.pem'
        for value in ['', 'invalid', self.config['ca_pem'] + (self.root / 'key.pem').read_text(),
                      'unexpected\n' + self.config['ca_pem'], self.config['ca_pem'] + 'unexpected',
                      self.config['ca_pem'] * 257, ' ' * ((512 << 10) + 1)]:
            path.write_text(value)
            with patch.dict(os.environ, {'STEGO_TEST_GATEWAY_INTERNAL_CA_FILE': str(path)}):
                with self.assertRaises(ValueError):
                    apply_internal_fixture(document, '1', '1')
            self.assertEqual(document, {'items': []})
        with patch.dict(os.environ, {'STEGO_TEST_GATEWAY_INTERNAL_CA_FILE': str(self.root / 'ca.pem')}):
            for workload, browser in [('0', '1'), ('1', '0')]:
                with self.assertRaises(ValueError):
                    apply_internal_fixture(document, workload, browser)

    def test_required_and_scoped_mount(self):
        job = {'items': [{'kind': 'Job', 'metadata': {'name': 'service-check'}, 'spec': {'template': {'spec': {
            'volumes': [], 'containers': [{'env': [], 'volumeMounts': []}]}}}}, {'kind': 'ConfigMap', 'metadata': {'name': 'database-ca'}, 'data': {'server.crt': 'separate database trust'}}]}
        with patch.dict(os.environ, {'STEGO_TEST_GATEWAY_PUBLIC_CONFIG': '', 'STEGO_TEST_REQUIRE_PUBLIC_GATEWAY': '0'}):
            original = copy.deepcopy(job)
            apply_public_fixture(job, 'stego-service-ci', '1', '1')
            self.assertEqual(job, original)
            os.environ['STEGO_TEST_REQUIRE_PUBLIC_GATEWAY'] = '1'
            with self.assertRaises(ValueError):
                apply_public_fixture(job, 'stego-service-ci', '1', '1')
            os.environ['STEGO_TEST_GATEWAY_PUBLIC_CONFIG'] = self.write(self.config)
            for workload, browser in [('0', '1'), ('1', '0')]:
                with self.assertRaises(ValueError):
                    apply_public_fixture(job, 'stego-service-ci', workload, browser)
            apply_public_fixture(job, 'stego-service-ci', '1', '1')
            with self.assertRaises(ValueError):
                apply_public_fixture(job, 'stego-service-ci', '1', '1')
        spec = job['items'][0]['spec']['template']['spec']
        self.assertEqual(spec['containers'][0]['volumeMounts'], [{'name': 'gateway-public-config', 'mountPath': '/gateway-public', 'readOnly': True}])
        self.assertEqual(job['items'][1]['kind'], 'ConfigMap')
        self.assertEqual(json.loads(job['items'][1]['data']['gateway-public.json']), self.config)
        spec['containers'][0]['env'] = []
        spec['containers'][0]['volumeMounts'] = []
        spec['volumes'] = []
        job['items'][1]['data'].pop('gateway-public.json')
        self.assertEqual(job, original)


if __name__ == '__main__':
    unittest.main()
