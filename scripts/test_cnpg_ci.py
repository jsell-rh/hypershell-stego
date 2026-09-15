"""Check the fixed CNPG CI authority and lifetime boundaries."""
import unittest
import tempfile
import json
from pathlib import Path
from unittest.mock import patch, Mock
from types import SimpleNamespace

import cnpg_ci as ci


class CNPGCIBoundary(unittest.TestCase):
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
        self.assertEqual(templates['database-job']['spec']['activeDeadlineSeconds'], 1500)
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
            result = browser / 'allocation-final.json'
            result.write_text('{"allocations_absent":true,"allocations_before":0}')
            with patch.object(runner, 'require_context_credentials', return_value=('private-cleanup-token', {'server': 'https://api.example'})), \
                 patch.object(runner.subprocess, 'run'):
                with self.assertRaises(FileNotFoundError):
                    runner.verify_allocations(browser)
            def complete(words, **options):
                self.assertNotIn('--remove', words)
                self.assertEqual(words[0], str(browser / 'allocation-cleanup'))
                self.assertEqual(options, {'check': True, 'timeout': 195})
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
            with patch.object(runner, 'require_context_credentials', return_value=('private-cleanup-token', {'server': 'https://api.example'})), \
                 patch.object(runner.subprocess, 'run', side_effect=complete):
                runner.verify_allocations(browser)
            self.assertEqual(len(seen), 1)
            self.assertFalse(seen[0].exists())

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
