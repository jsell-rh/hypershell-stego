"""Check the CNPG lock handoff and private fixture projection."""
import importlib.util
import json
import io
import tarfile
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


lock = load('cnpg_lock_test', 'jshell_live_lock.py')
fixture = load('cnpg_projection_test', 'render-service-fixture.py')
runner = load('cnpg_runner_test', 'check-cnpg-installation.py')


class CNPGWorkflowTests(unittest.TestCase):
    def lease(self):
        return {'metadata': {'uid': 'original', 'annotations': {'stego.test/namespace': 'stego-service-ci', 'stego.test/job': 'service-check'}},
                'spec': {'holderIdentity': 'cnpg-run'}}

    def test_lock_handoff_is_read_only_and_checks_job_absence(self):
        with patch.object(lock, 'lease', return_value=self.lease()), patch.object(lock, 'oc', return_value=None) as oc:
            lock.verify('explicit-context', 'cnpg-run', 'original', 'stego-service-ci', 'service-check')
            oc.assert_called_once_with('explicit-context', '-n', 'stego-service-ci', 'get', 'job', 'service-check', '--ignore-not-found', '-o', 'json')
        with patch.object(lock, 'lease', return_value=self.lease()), patch.object(lock, 'oc', return_value={'metadata': {'uid': 'existing'}}):
            with self.assertRaisesRegex(RuntimeError, 'already has a test Job'):
                lock.verify('explicit-context', 'cnpg-run', 'original', 'stego-service-ci', 'service-check')

    def test_changed_lease_or_target_cannot_be_adopted(self):
        for change in [
            lambda v: v['metadata'].update(uid='replacement'),
            lambda v: v['spec'].update(holderIdentity='other'),
            lambda v: v['metadata']['annotations'].update({'stego.test/namespace': 'other'}),
            lambda v: v['metadata']['annotations'].update({'stego.test/job': 'other'}),
        ]:
            value = self.lease()
            change(value)
            with patch.object(lock, 'lease', return_value=value), patch.object(lock, 'oc') as oc:
                with self.assertRaisesRegex(RuntimeError, 'identity or test target differs'):
                    lock.verify('explicit-context', 'cnpg-run', 'original', 'stego-service-ci', 'service-check')
                oc.assert_not_called()
        with self.assertRaises(RuntimeError):
            lock.verify('explicit-context', '', '', 'stego-service-ci', 'service-check')

    def render(self, directory, enabled):
        root = Path(directory)
        acceptance = root / 'acceptance'
        acceptance.mkdir(exist_ok=True)
        for name in ['kubernetes-service-job.json', 'browser-workload-rbac.json']:
            (acceptance / name).write_bytes((ROOT / 'acceptance' / name).read_bytes())
        (acceptance / 'browser-inspection-source.json').write_text(json.dumps({'cnpg_installation': {'cluster': 'gateway-database'}}))
        (root / 'kubernetes-endpoints.json').write_text(json.dumps({'items': [{'endpoints': [{'conditions': {'ready': True}, 'addresses': ['192.0.2.1']}], 'ports': [{'protocol': 'TCP', 'name': 'https', 'port': 443}]}]}))
        (root / 'kubernetes-service.json').write_text(json.dumps({'spec': {'clusterIP': '192.0.2.2'}}))
        with patch.object(fixture, 'PROJECT', root), patch.dict(os.environ, {'STEGO_TEST_CNPG_FIXTURE': enabled}):
            return fixture.fixture('stego-service-ci', root, '1', '1', 'test-ca')

    def test_only_test_container_receives_the_private_cnpg_file(self):
        with tempfile.TemporaryDirectory() as directory:
            before = self.render(directory, '0')
            after = self.render(directory, '1')
        original = next(o for o in before['items'] if o['kind'] == 'Job')['spec']['template']['spec']
        changed = next(o for o in after['items'] if o['kind'] == 'Job')['spec']['template']['spec']
        self.assertEqual(changed['volumes'].pop(), {'name': 'cnpg-fixture', 'secret': {'secretName': 'cnpg-credentials', 'defaultMode': 0o440}})
        test = changed['containers'][0]
        self.assertEqual(test['volumeMounts'].pop(), {'name': 'cnpg-fixture', 'mountPath': '/cnpg-installation', 'readOnly': True})
        self.assertEqual(test['env'].pop(), {'name': 'STEGO_TEST_GATEWAY_SQL_FIXTURE_FILE', 'value': '/cnpg-installation/fixture.json'})
        self.assertEqual(original, changed)
        self.assertEqual(before, after)
        self.assertFalse(any(o['kind'] == 'Secret' and o['metadata']['name'] == 'cnpg-credentials' for o in after['items']))

    def evidence(self, directory, change=None):
        root = Path(directory)
        installation = {'namespace': 'stego-cnpg-database-ci', 'namespace_uid': 'namespace-uid', 'cluster_uid': 'cluster-uid'}
        restart = dict(installation, cluster_spec_unchanged=True, sql_object_ids_unchanged=True,
                       gateway_credentials_and_keys_unchanged=True, provider_data_unchanged=True, installation_data_unchanged=True,
                       old_pod_uid='old', new_primary_pod_uid='new', ready_instances=2, seconds=30,
                       old_primary='gateway-database-1', new_primary='gateway-database-2', primary_changed=True)
        if change:
            change(restart)
        (root / 'job-status.json').write_text(json.dumps({'status': {'conditions': [{'type': 'Complete', 'status': 'True'}]}}))
        (root / 'deployment.log').write_text('--- PASS: TestGeneratedKubernetesBrowserGatewayWorkflow (400.00s)\n')
        (root / 'cleanup.json').write_text(json.dumps({'namespace_retained': 'stego-service-ci', 'test_resources_absent': True, 'allocations_absent': True}))
        with tarfile.open(root / 'evidence.tar', 'w') as archive:
            for name, value in {'deployment.exit': b'0\n', 'first.sha256': b'generated\n', 'second.sha256': b'generated\n',
                                'after-tests.sha256': b'generated\n', 'browser-artifacts/cnpg-restart.json': json.dumps(restart).encode()}.items():
                item = tarfile.TarInfo(name)
                item.size = len(value)
                archive.addfile(item, io.BytesIO(value))
        return installation

    def test_result_requires_cnpg_identity_recovery_and_complete_job(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            installation = self.evidence(directory)
            runner.verify_application(root, installation)
            for change in [lambda r: r.update(cluster_uid='foreign'), lambda r: r.update(new_primary_pod_uid='old'),
                           lambda r: r.update(primary_changed=False), lambda r: r.update(ready_instances=1),
                           lambda r: r.update(installation_data_unchanged=False)]:
                installation = self.evidence(directory, change)
                with self.assertRaises(RuntimeError):
                    runner.verify_application(root, installation)
            installation = self.evidence(directory)
            (root / 'job-status.json').write_text(json.dumps({'status': {'conditions': [{'type': 'Failed', 'status': 'True'}]}}))
            with self.assertRaisesRegex(RuntimeError, 'Job did not complete'):
                runner.verify_application(root, installation)
            installation = self.evidence(directory)
            (root / 'deployment.log').write_text('--- FAIL: TestGeneratedKubernetesBrowserGatewayWorkflow (400.00s)\n')
            with self.assertRaisesRegex(RuntimeError, 'workflow did not pass'):
                runner.verify_application(root, installation)


    def test_invalid_fixture_flag_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, 'CNPG fixture flag'):
                self.render(directory, 'true')


if __name__ == '__main__':
    unittest.main()
