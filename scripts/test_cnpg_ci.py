"""Check the fixed CNPG CI authority and lifetime boundaries."""
import unittest
import copy
import tempfile
import json
import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch, Mock
from types import SimpleNamespace

import cnpg_ci as ci


class CNPGCIBoundary(unittest.TestCase):
    def test_dependencies_outlive_the_application_job(self):
        from ci_credentials import CNPG_SECONDS, CNPG_RUNTIME_SECONDS
        objects, templates = self.build()
        application = json.loads((Path(__file__).resolve().parent.parent / 'acceptance/kubernetes-service-job.json').read_text())
        job = next(item for item in application['items'] if item['kind'] == 'Job')
        application_seconds = job['spec']['activeDeadlineSeconds']
        self.assertEqual(application_seconds, 1800)
        # Include the six-minute database readiness wait and three minutes for cleanup.
        self.assertGreaterEqual(CNPG_RUNTIME_SECONDS, application_seconds + 360 + 180)
        self.assertLess(CNPG_RUNTIME_SECONDS, CNPG_SECONDS)
        for name in ['database-job', 'operator-job']:
            self.assertEqual(templates[name]['spec']['activeDeadlineSeconds'], CNPG_RUNTIME_SECONDS)
        for namespace in [ci.OPERATOR_NS, ci.DATABASE_NS]:
            policy = next(item for item in objects if item['kind'] == 'ValidatingAdmissionPolicy' and item['metadata']['name'] == namespace + '.bounded-jobs')
            rules = [item['expression'] for item in policy['spec']['validations'] if 'activeDeadlineSeconds' in item['expression']]
            self.assertTrue(any('activeDeadlineSeconds <= ' + str(CNPG_RUNTIME_SECONDS) in rule for rule in rules))

    def build(self):
        fixture = [
            {'kind': 'ClusterRole', 'metadata': {'name': 'cnpg-manager'}, 'rules': [
                {'apiGroups': [''], 'resources': ['pods', 'secrets', 'nodes'], 'verbs': ['create', 'get', 'list', 'watch', 'delete']},
                {'apiGroups': ['admissionregistration.k8s.io'], 'resources': ['validatingwebhookconfigurations'], 'verbs': ['get', 'patch']}]},
            {'apiVersion': 'apiextensions.k8s.io/v1', 'kind': 'CustomResourceDefinition', 'metadata': {'name': 'clusters.postgresql.cnpg.io'}, 'spec': {}},
            {'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingWebhookConfiguration', 'metadata': {'name': 'cnpg-validating'},
             'webhooks': [{'name': 'clusters.cnpg.io', 'clientConfig': {'service': {'name': 'cnpg-webhook-service', 'namespace': 'cnpg-system'}}}]},
            {'kind': 'Deployment', 'spec': {'template': {'metadata': {}, 'spec': {'containers': [{
                'name': 'manager', 'image': 'old', 'command': ['/manager'], 'args': ['controller', '--max-concurrent-reconciles=10'],
                'env': [], 'securityContext': {}, 'resources': {}}], 'volumes': []}}}},
        ]
        return ci.build(fixture, [('10.0.0.1', 443)], 'existing-issuer', 'gp3-csi')

    def test_ci_has_no_cluster_write_or_webhook_authority(self):
        objects, templates = self.build()
        roles = {o['metadata']['name']: o for o in objects if o['kind'] == 'ClusterRole'}
        observer = roles['stego-cnpg-ci-observer']
        self.assertTrue(all(set(r['verbs']) <= {'get', 'list', 'watch'} for r in observer['rules']))
        self.assertFalse(any('webhook' in name for o in roles.values() for r in o['rules'] for name in r['resources']))
        bindings = [o for o in objects if o['kind'] == 'ClusterRoleBinding']
        ci_bindings = [o for o in bindings if ci.service_account('hypershell-ci', 'stego-ci-access') in o['subjects']]
        self.assertEqual(len(ci_bindings), 1)
        self.assertEqual(ci_bindings[0]['roleRef']['name'], 'stego-cnpg-ci-installation-reader')
        self.assertTrue(all(set(r['verbs']) <= {'get', 'list'} for r in roles['stego-cnpg-ci-installation-reader']['rules']))
        ci_roles = [o for o in objects if o['kind'] == 'Role' and o['metadata']['name'] == 'cnpg-ci']
        self.assertEqual({o['metadata']['namespace'] for o in ci_roles}, {ci.OPERATOR_NS, ci.DATABASE_NS})
        network_reads = [(o['metadata']['namespace'], r) for o in ci_roles for r in o['rules'] if 'networkpolicies' in r['resources']]
        self.assertEqual(network_reads, [(ci.DATABASE_NS, {'apiGroups': ['networking.k8s.io'], 'resources': ['networkpolicies'], 'resourceNames': ['database'], 'verbs': ['get']})])
        self.assertFalse(any(resource in {'roles', 'rolebindings', 'namespaces', 'certificates', 'deployments'} and not set(r['verbs']) <= {'get', 'list'} for o in ci_roles for r in o['rules'] for resource in r['resources']))

    def test_operator_uses_supplied_certificate_and_bounded_job(self):
        objects, templates = self.build()
        job = templates['operator-job']
        self.assertEqual(job['spec']['activeDeadlineSeconds'], 2400)
        self.assertEqual(job['spec']['backoffLimit'], 0)
        self.assertEqual(job['spec']['ttlSecondsAfterFinished'], 0)
        pod = job['spec']['template']['spec']
        self.assertEqual(pod['restartPolicy'], 'Never')
        env = {e['name']: e['value'] for e in pod['containers'][0]['env']}
        self.assertEqual(env['WATCH_NAMESPACE'], ci.DATABASE_NS)
        self.assertEqual(env['WEBHOOK_CERT_DIR'], '/etc/cnpg-webhook')
        self.assertEqual(env['MANAGE_WEBHOOK_CONFIGURATIONS'], 'false')
        self.assertEqual(pod['containers'][0]['resources']['limits']['memory'], '512Mi')
        self.assertEqual(templates['database-job']['spec']['activeDeadlineSeconds'], 2400)
        self.assertEqual(templates['cluster']['spec']['instances'], 2)
        webhook = next(o for o in objects if o['kind'] == 'ValidatingWebhookConfiguration')
        self.assertEqual(webhook['webhooks'][0]['namespaceSelector'], {'matchLabels': {'kubernetes.io/metadata.name': ci.DATABASE_NS}})
        self.assertEqual(webhook['webhooks'][0]['failurePolicy'], 'Fail')
        self.assertEqual(webhook['metadata']['annotations']['cert-manager.io/inject-ca-from'], ci.OPERATOR_NS + '/webhook')
        self.assertEqual(webhook['webhooks'][0]['clientConfig']['service']['namespace'], ci.OPERATOR_NS)
        self.assertFalse(any(o['kind'] in {'Deployment', 'Job', 'Cluster'} for o in objects))

    def test_policy_fixes_operator_image_command_and_scope(self):
        objects, templates = self.build()
        policies = [o for o in objects if o['kind'] == 'ValidatingAdmissionPolicy']
        self.assertEqual(len(policies), 3)
        operator = next(o for o in policies if o['metadata']['name'] == ci.OPERATOR_NS + '.bounded-jobs')
        rules = '\n'.join(r['expression'] for r in operator['spec']['validations'])
        self.assertIn(templates['operator-job']['spec']['template']['spec']['containers'][0]['image'], rules)
        self.assertIn('c.command == ["/manager"]', rules)
        self.assertIn('WATCH_NAMESPACE', rules)
        self.assertIn('MANAGE_WEBHOOK_CONFIGURATIONS', rules)
        self.assertIn('!object.spec.suspend', rules)
        self.assertIn('activeDeadlineSeconds <= 2400', rules)
        self.assertEqual(operator['spec']['matchConstraints']['namespaceSelector'], {'matchLabels': {'kubernetes.io/metadata.name': ci.OPERATOR_NS}})
        # These policies must not block CNPG's own bootstrap Jobs.
        self.assertEqual(len(operator['spec']['matchConditions']), 1)
        self.assertIn('hypershell-ci', operator['spec']['matchConditions'][0]['expression'])

    def test_lifetime_pod_has_no_code_or_network_extension(self):
        objects, templates = self.build()
        network = next(o for o in objects if o['kind'] == 'NetworkPolicy' and o['metadata']['name'] == 'default-deny')
        self.assertEqual(network['metadata']['namespace'], ci.DATABASE_NS)
        self.assertEqual(network['spec'], {'podSelector': {}, 'policyTypes': ['Ingress', 'Egress']})
        policy = next(o for o in objects if o['kind'] == 'ValidatingAdmissionPolicy' and o['metadata']['name'] == ci.DATABASE_NS + '.bounded-jobs')
        rules = '\n'.join(v['expression'] for v in policy['spec']['validations'])
        self.assertIn(templates['database-job']['spec']['template']['spec']['containers'][0]['image'], rules)
        self.assertIn('c.command == ["/bin/sleep", "3600"]', rules)
        self.assertIn('!has(c.env)', rules)
        self.assertIn('!has(c.lifecycle)', rules)
        self.assertIn('!has(c.livenessProbe)', rules)
        self.assertIn('!("cnpg.io/cluster" in object.spec.template.metadata.labels)', rules)


class RuntimeCleanupBoundary(unittest.TestCase):
    def test_database_network_policy_rejects_stale_or_wider_installation(self):
        runner = ci.module('cnpg_ci_network_policy', 'check-cnpg-ci.py')
        policy = ci.database_network_policy([('10.0.0.1', 443)])
        policy['metadata'].update(uid='policy-uid', resourceVersion='1', labels=dict(ci.OWNER))
        installation = {'resources': [{'kind': 'NetworkPolicy', 'name': 'database', 'namespace': ci.DATABASE_NS, 'uid': 'policy-uid'}]}
        client = Mock()
        client.get.return_value = policy
        result = runner.verify_database_network_policy(client, installation, [('10.0.0.1', 443)])
        self.assertEqual(result['spec'], policy['spec'])
        self.assertEqual(len(result['spec_sha256']), 64)
        for change in ('stale', 'wide', 'different-owner', 'different-uid'):
            value = copy.deepcopy(policy)
            if change == 'stale':
                value['spec']['ingress'][-1]['from'] = [peer for peer in value['spec']['ingress'][-1]['from'] if peer.get('podSelector') != {'matchLabels': {'app.kubernetes.io/name': 'hypershell-gateway-console'}}]
            elif change == 'wide':
                value['spec']['ingress'].append({})
            elif change == 'different-owner':
                value['metadata']['labels'] = {}
            else:
                value['metadata']['uid'] = 'replacement'
            client.get.return_value = value
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, 'network policy differs'):
                runner.verify_database_network_policy(client, installation, [('10.0.0.1', 443)])

    def test_missing_or_invalid_public_gateway_stops_before_cluster_access(self):
        runner = ci.module('cnpg_ci_public_preflight', 'check-cnpg-ci.py')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); config = root / 'public.json'; results = root / 'results'
            argv = ['check-cnpg-ci.py', '--source', str(root), '--repository', str(root),
                    '--kubeconfig', str(root / 'credentials'), '--results', str(results)]
            for data in [None, '', '[]', '{}', '{"domain":"example.test","domain":"other.test"}']:
                if data is not None:
                    config.write_text(data)
                with self.subTest(data=data), patch.dict(os.environ, {'STEGO_TEST_GATEWAY_PUBLIC_CONFIG': '' if data is None else str(config)}), \
                     patch.object(sys, 'argv', argv), patch.object(runner, 'gateway_ca_input', return_value=b'validated CA'), \
                     patch.object(runner, 'require_context_credentials') as credentials, patch.object(runner.ci, 'module') as module:
                    with self.assertRaises(ValueError):
                        runner.main()
                    credentials.assert_not_called()
                    module.assert_not_called()
                    self.assertFalse(results.exists())

    def test_invalid_gateway_ca_stops_before_cluster_access(self):
        runner = ci.module('cnpg_ci_ca_preflight', 'check-cnpg-ci.py')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ca = root / 'ca.pem'
            results = root / 'results'
            argv = ['check-cnpg-ci.py', '--source', str(root), '--repository', str(root),
                    '--kubeconfig', str(root / 'credentials'), '--results', str(results)]
            for data in [None, b'', b'not a certificate', b'PRIVATE KEY', b'x' * ((64 << 10) + 1)]:
                if data is not None:
                    ca.write_bytes(data)
                with self.subTest(data=None if data is None else len(data)), \
                     patch.dict(os.environ, {'STEGO_TEST_GATEWAY_INTERNAL_CA_FILE': '' if data is None else str(ca)}), \
                     patch.object(sys, 'argv', argv), patch.object(runner, 'require_context_credentials') as credentials, \
                     patch.object(runner.ci, 'module') as module:
                    with self.assertRaises(ValueError):
                        runner.main()
                    credentials.assert_not_called()
                    module.assert_not_called()
                    self.assertFalse(results.exists())

    def test_gateway_ca_is_validated_and_captured_before_use(self):
        runner = ci.module('cnpg_ci_ca_snapshot', 'check-cnpg-ci.py')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); ca = root / 'ca.pem'
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256',
                            '-nodes', '-keyout', str(root / 'key.pem'), '-out', str(ca), '-subj', '/CN=CNPG test CA', '-days', '1'],
                           capture_output=True, check=True, timeout=10)
            expected = ca.read_bytes()
            captured = runner.gateway_ca_input(str(ca))
            ca.write_text('changed input')
            self.assertEqual(captured, expected)

    def test_outer_allocation_check_needs_no_browser_child_files(self):
        runner = ci.module('cnpg_ci_outer_allocation', 'check-cnpg-ci.py')
        installation = {'metadata': {'labels': {'app.kubernetes.io/managed-by': 'stego-browser-ci'}},
                        'immutable': True, 'data': {'namespace-uid': 'expected',
                        'kubernetes-endpoints.json': json.dumps({'items': [{'endpoints': [{'conditions': {'ready': True}, 'addresses': ['192.0.2.1']}], 'ports': [{'name': 'https', 'protocol': 'TCP', 'port': 6443}]}]}),
                        'kubernetes-service.json': json.dumps({'spec': {'clusterIP': '10.0.0.1'}})}}
        client = Mock()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); outer = root / 'allocation-check'
            client.get.side_effect = [installation, {'metadata': {'uid': 'expected'}}]
            with patch.object(runner.subprocess, 'run') as build:
                self.assertEqual(runner.prepare_allocation_check(root, outer, client), outer)
                self.assertEqual(build.call_args.args[0][:3], ['go', 'build', '-p=1'])
                self.assertEqual(build.call_args.kwargs['timeout'], 60)
            self.assertFalse((root / 'browser').exists())
            def complete(words, **options):
                self.assertEqual(words[0], str(outer / 'allocation-cleanup'))
                self.assertNotIn('--remove', words)
                self.assertEqual(json.loads(options['env']['STEGO_ALLOCATION_NETWORK_ENDPOINTS']),
                                 {'kubernetes': ['10.0.0.1:443', '192.0.2.1:6443']})
                Path(words[words.index('--result') + 1]).write_text('{"allocations_absent":true,"allocations_before":0}')
            with patch.object(runner, 'require_context_credentials', return_value=('test-private-token', {'server': 'https://api.example'})), \
                 patch.object(runner.subprocess, 'run', side_effect=complete):
                runner.verify_allocations(outer)
            client.create.assert_not_called()
            client.oc.assert_not_called()
            for change in [lambda: installation.update(immutable=False),
                           lambda: installation['data'].update({'namespace-uid': 'foreign'}),
                           lambda: installation['data'].update({'kubernetes-endpoints.json': '{"items":[]}'})]:
                installation['immutable'] = True
                installation['data']['namespace-uid'] = 'expected'
                change()
                client.get.side_effect = [installation, {'metadata': {'uid': 'expected'}}]
                target = root / ('invalid-' + str(len(list(root.iterdir()))))
                with patch.object(runner.subprocess, 'run') as build, self.assertRaises((ValueError, RuntimeError)):
                    runner.prepare_allocation_check(root, target, client)
                build.assert_not_called()

    def test_deletion_uses_api_resource_names_and_identity_preconditions(self):
        runner = ci.module('cnpg_ci_delete_boundary', 'check-cnpg-ci.py')
        for version, kind, resource, prefix, api_resource in [('postgresql.cnpg.io/v1', 'Cluster', 'clusters.postgresql.cnpg.io', '/apis/postgresql.cnpg.io/v1', 'clusters'),
                                                ('v1', 'PersistentVolumeClaim', 'persistentvolumeclaims', '/api/v1', 'persistentvolumeclaims')]:
            value = {'apiVersion': version, 'kind': kind, 'metadata': {'name': 'owned', 'namespace': 'database',
                'uid': 'same-uid', 'resourceVersion': 'fresh-version', 'annotations': {'stego.test/cnpg-holder': 'held'}}}
            client = Mock()
            runner.delete_owned(client, value, resource, 'held', 'cluster-uid')
            client.oc.assert_called_once_with('delete', '--raw=' + prefix + '/namespaces/database/' + api_resource + '/owned', '-f', '-',
                data={'apiVersion': 'v1', 'kind': 'DeleteOptions', 'preconditions': {'uid': 'same-uid', 'resourceVersion': 'fresh-version'}, 'propagationPolicy': 'Foreground'})
            client.reset_mock()
            with self.assertRaisesRegex(RuntimeError, 'unowned'):
                runner.delete_owned(client, value, resource, 'different-holder', 'cluster-uid')
            client.oc.assert_not_called()

    def test_final_allocation_check_uses_the_scoped_client_and_new_evidence(self):
        runner = ci.module('cnpg_ci_allocation_boundary', 'check-cnpg-ci.py')
        with tempfile.TemporaryDirectory() as directory:
            browser = Path(directory)
            (browser / 'ci-server').write_text('https://api.example')
            (browser / 'ci-ca.pem').write_text('')
            (browser / 'kubernetes-endpoints.json').write_text(json.dumps({'items': [{
                'endpoints': [{'conditions': {'ready': True}, 'addresses': ['192.0.2.1']}],
                'ports': [{'protocol': 'TCP', 'name': 'https', 'port': 6443}]}]}))
            (browser / 'kubernetes-service.json').write_text(json.dumps({'spec': {'clusterIP': '10.0.0.1'}}))
            result = browser / 'allocation-final.json'
            result.write_text('{"allocations_absent":true,"allocations_before":0}')
            with patch.object(runner, 'require_context_credentials', return_value=('private-cleanup-token', {'server': 'https://api.example'})), \
                 patch.object(runner.subprocess, 'run'):
                with self.assertRaises(FileNotFoundError):
                    runner.verify_allocations(browser)
            def complete(words, **options):
                self.assertNotIn('--remove', words)
                self.assertEqual(words[0], str(browser / 'allocation-cleanup'))
                environment = options.pop('env')
                self.assertEqual(options, {'check': True, 'timeout': 195})
                self.assertEqual(json.loads(environment['STEGO_ALLOCATION_NETWORK_ENDPOINTS']),
                                 {'kubernetes': ['10.0.0.1:443', '192.0.2.1:6443']})
                token_file = Path(words[words.index('--token-file') + 1])
                self.assertEqual(token_file.read_text(), 'private-cleanup-token')
                self.assertEqual(token_file.stat().st_mode & 0o777, 0o600)
                self.assertNotIn('private-cleanup-token', words)
                self.assertNotEqual(token_file, browser / 'ci-token')
                seen.append(token_file)
                result.write_text(json.dumps({'allocations_absent': True, 'allocations_before': 0}))
            seen = []
            # The browser child removed its token. The outer check must use its
            # own restricted credential and remove its temporary copy afterward.
            with patch.dict(os.environ, {'STEGO_ALLOCATION_NETWORK_ENDPOINTS': '{"kubernetes":["192.0.2.99:443"]}'}), \
                 patch.object(runner, 'require_context_credentials', return_value=('private-cleanup-token', {'server': 'https://api.example'})), \
                 patch.object(runner.subprocess, 'run', side_effect=complete):
                runner.verify_allocations(browser)
            self.assertEqual(len(seen), 1)
            self.assertFalse(seen[0].exists())

    def test_final_allocation_check_requires_the_saved_endpoint_set(self):
        runner = ci.module('cnpg_ci_allocation_endpoints', 'check-cnpg-ci.py')
        with tempfile.TemporaryDirectory() as directory:
            browser = Path(directory)
            result = browser / 'allocation-final.json'
            for snapshot in [None, {'items': []}]:
                result.write_text('{"allocations_absent":true,"allocations_before":0}')
                if snapshot is not None:
                    (browser / 'kubernetes-endpoints.json').write_text(json.dumps(snapshot))
                with patch.object(runner, 'require_context_credentials') as credentials, \
                     patch.object(runner.subprocess, 'run') as run:
                    with self.assertRaises((FileNotFoundError, ValueError)):
                        runner.verify_allocations(browser)
                credentials.assert_not_called()
                run.assert_not_called()
                self.assertFalse(result.exists())

    def test_exec_probe_uses_the_subresource_flag(self):
        runner = ci.module('cnpg_ci_exec_boundary', 'check-cnpg-ci.py')
        with patch.object(runner.subprocess, 'run') as run:
            runner.access_probe('jshell-ci', 'create', 'pods/exec', ci.OPERATOR_NS)
        words = run.call_args.args[0]
        self.assertEqual(words[words.index('can-i') + 1:],
                         ['create', 'pods', '--subresource=exec', '-n', ci.OPERATOR_NS])

    def test_named_config_probe_retains_the_resource_name(self):
        runner = ci.module('cnpg_ci_config_boundary', 'check-cnpg-ci.py')
        with patch.object(runner.subprocess, 'run') as run:
            runner.access_probe('jshell-ci', 'delete', 'configmaps/' + ci.CONFIG, ci.APP_NS)
        words = run.call_args.args[0]
        self.assertEqual(words[words.index('can-i') + 1:],
                         ['delete', 'configmaps/' + ci.CONFIG, '-n', ci.APP_NS])

    def test_a_failed_request_is_not_an_admission_denial(self):
        runner = ci.module('cnpg_ci_admission_boundary', 'check-cnpg-ci.py')
        value = {'kind': 'Job'}
        for response in [
            SimpleNamespace(returncode=0, stdout='{}', stderr=''),
            SimpleNamespace(returncode=1, stdout='', stderr='Forbidden'),
            SimpleNamespace(returncode=1, stdout='', stderr='policy denied request: other-policy'),
        ]:
            with patch.object(runner.subprocess, 'run', return_value=response):
                with self.assertRaises(RuntimeError):
                    runner.admission_probe('jshell-ci', value, 'fixed-policy', False)
        response = SimpleNamespace(returncode=1, stdout='', stderr='fixed-policy denied request: fixed boundary')
        with patch.object(runner.subprocess, 'run', return_value=response):
            runner.admission_probe('jshell-ci', value, 'fixed-policy', False)

    def test_cleanup_requires_the_recorded_owner(self):
        runner = ci.module('cnpg_ci_runtime_boundary', 'check-cnpg-ci.py')
        own = {'metadata': {'annotations': {'stego.test/cnpg-holder': 'ours'}}}
        self.assertTrue(runner.resource_owned(own, 'ours', 'cluster'))
        self.assertFalse(runner.resource_owned(own, 'other', 'cluster'))
        child = {'metadata': {'ownerReferences': [{'uid': 'cluster', 'kind': 'Cluster', 'apiVersion': 'postgresql.cnpg.io/v1'}]}}
        self.assertTrue(runner.resource_owned(child, 'ours', 'cluster'))
        child['metadata']['ownerReferences'][0]['kind'] = 'Job'
        self.assertFalse(runner.resource_owned(child, 'ours', 'cluster'))
        self.assertFalse(runner.resource_owned({'metadata': {}}, '', ''))

    def test_database_cleanup_keeps_the_lifetime_pod_until_last(self):
        runner = ci.module('cnpg_ci_pod_boundary', 'check-cnpg-ci.py')
        database = {'metadata': {'name': 'database', 'labels': {'cnpg.io/cluster': 'gateway-database'}}}
        lifetime = {'metadata': {'name': 'deadline', 'labels': {'job-name': 'database-lifetime'}}}
        self.assertEqual(runner.database_pods([database, lifetime]), [database])
        self.assertEqual(runner.database_pods([lifetime]), [])


if __name__ == '__main__':
    unittest.main()
