"""Check the CNPG lock handoff and private fixture projection."""
import importlib.util
import copy
import hashlib
import json
import io
import tarfile
import os
from pathlib import Path
import tempfile
import subprocess
import sys
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
inspection = load('cnpg_source_inspection_test', 'prepare-browser-inspection.py')


class CNPGWorkflowTests(unittest.TestCase):
    def test_frozen_source_checks_cnpg_peer_and_inspection_permissions_together(self):
        runtime = 'out/deploy/allocation/allocation.go'
        original = inspection.allocation_config((ROOT / runtime).read_text())
        config = copy.deepcopy(original)
        config['Roles'] = inspection.inspection_roles() + config['Roles']
        for profile in config['Profiles']:
            role = {'gateway': 'fixture-gateway-inspector', 'gateway-state': 'fixture-state-inspector',
                    'gateway-console-state': 'fixture-console-state-inspector'}.get(profile['Name'])
            if role is None:
                continue
            profile['Bindings'].append({'Role': role, 'ExternalRole': '', 'ServiceAccount': 'service-check', 'Namespace': 'control', 'ExternalNamespace': ''})
            if profile['Name'] == 'gateway':
                profile['NetworkPeers'].insert(0, {'Direction': 'egress', 'Namespace': 'external',
                    'ExternalNamespace': 'stego-cnpg-database-ci', 'PodLabel': 'cnpg.io/cluster',
                    'PodValue': 'gateway-database', 'Protocol': 'TCP', 'Port': 5432})
        def go(value):
            return 'json.Unmarshal([]byte(' + json.dumps(json.dumps(value)) + '), &config)'
        payloads = {'service.yaml': (ROOT / 'service.yaml').read_text(), runtime: go(original),
                    '.stego/compiler-revision': 'a' * 40, '.stego/state.yaml': 'original',
                    'out/deploy/render/worker-namespace-allocation.json.tmpl': 'original',
                    'out/deploy/render/worker-gateway-workload.json.tmpl': 'original'}
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as files:
            for name, text in payloads.items():
                data = text.encode(); item = tarfile.TarInfo(name); item.size = len(data)
                files.addfile(item, io.BytesIO(data))
        changed = set(payloads) - {'.stego/compiler-revision'}
        def gateway(value):
            return next(p for p in value['Profiles'] if p['Name'] == 'gateway')
        cases = [
            ('valid', lambda c: None, ''),
            ('foreign namespace', lambda c: gateway(c)['NetworkPeers'][0].update(ExternalNamespace='default'), ''),
            ('all ports', lambda c: gateway(c)['NetworkPeers'][0].update(Port=0), ''),
            ('missing peer', lambda c: gateway(c)['NetworkPeers'].pop(0), ''),
            ('extra peer', lambda c: gateway(c)['NetworkPeers'].append(copy.deepcopy(gateway(c)['NetworkPeers'][0])), ''),
            ('broader role', lambda c: c['Roles'][0]['Rules'][0].pop('resourceNames'), ''),
            ('changed runtime', lambda c: None, '; changed code'),
        ]
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory)
            (source / 'acceptance').mkdir()
            for label, mutate, suffix in cases:
                modified = copy.deepcopy(config); mutate(modified)
                fixture_files = dict(payloads)
                for name in changed:
                    fixture_files[name] = 'fixture'
                fixture_files[runtime] = go(modified) + suffix
                fixture_files['service.yaml'] = inspection.cnpg_declaration(
                    inspection.declaration(payloads['service.yaml']), 'stego-cnpg-database-ci')
                for name, text in fixture_files.items():
                    path = source / name; path.parent.mkdir(parents=True, exist_ok=True); path.write_text(text)
                hashes = lambda values: {name: hashlib.sha256(text.encode()).hexdigest() for name, text in values.items()}
                record = {'source_base_commit': 'b' * 40, 'compiler_revision': 'a' * 40,
                          'source_sha256': hashes(payloads), 'fixture_sha256': hashes(fixture_files),
                          'changed_files': sorted(changed),
                          'cnpg_installation': {'namespace': 'stego-cnpg-database-ci', 'cluster': 'gateway-database'}}
                (source / 'acceptance/browser-inspection-source.json').write_text(json.dumps(record))
                with self.subTest(case=label), patch.object(runner.subprocess, 'check_output', return_value=archive.getvalue()), patch.object(runner, 'module', return_value=inspection):
                    if label == 'valid':
                        self.assertEqual(runner.verify_source(source, ROOT), record)
                    else:
                        with self.assertRaises(ValueError):
                            runner.verify_source(source, ROOT)

    def test_runner_import_keeps_frozen_source_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ['check-cnpg-installation.py', 'ci_credentials.py']:
                (root / name).write_bytes((ROOT / 'scripts' / name).read_bytes())
            environment = dict(os.environ)
            environment.pop('PYTHONDONTWRITEBYTECODE', None)
            environment.pop('PYTHONPYCACHEPREFIX', None)
            result = subprocess.run([sys.executable, str(root / 'check-cnpg-installation.py'), '--help'],
                                    cwd=root, env=environment, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0)
            self.assertEqual({p.name for p in root.iterdir()}, {'check-cnpg-installation.py', 'ci_credentials.py'})

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
        # The shared network profile does not select CNPG credentials. The
        # external database test must keep its original SQL fixture.
        self.assertFalse(any(v['name'] == 'cnpg-fixture' for v in original['volumes']))
        self.assertFalse(any(v['name'] == 'STEGO_TEST_GATEWAY_SQL_FIXTURE_FILE'
                             for v in original['containers'][0]['env']))
        self.assertEqual(changed['volumes'].pop(), {'name': 'cnpg-fixture', 'secret': {'secretName': 'cnpg-credentials', 'defaultMode': 0o440}})
        test = changed['containers'][0]
        self.assertEqual(test['volumeMounts'].pop(), {'name': 'cnpg-fixture', 'mountPath': '/cnpg-installation', 'readOnly': True})
        self.assertEqual(test['env'].pop(), {'name': 'STEGO_TEST_GATEWAY_SQL_FIXTURE_FILE', 'value': '/cnpg-installation/fixture.json'})
        self.assertEqual(original, changed)
        self.assertEqual(before, after)
        self.assertFalse(any(o['kind'] == 'Secret' and o['metadata']['name'] == 'cnpg-credentials' for o in after['items']))

    def evidence(self, directory, change=None, dashboard=None):
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
            values = {'deployment.exit': b'0\n', 'first.sha256': b'generated\n', 'second.sha256': b'generated\n',
                      'after-tests.sha256': b'generated\n', 'browser-artifacts/cnpg-restart.json': json.dumps(restart).encode()}
            values.update({'browser-artifacts/gateway-dashboard/' + name: json.dumps(value).encode() for name, value in (dashboard or {}).items()})
            for name, value in values.items():
                item = tarfile.TarInfo(name)
                item.size = len(value)
                archive.addfile(item, io.BytesIO(value))
        return installation

    def test_public_gate_requires_dashboard_editor_recovery_and_logout(self):
        phases = {'dashboard-' + phase + '.json': {'id': 'a' * 27, 'workspace': 'rendered-dashboard', 'verified': True}
                  for phase in ['create', 'reload', 'verify']}
        editor = 'dashboard-create.json.editor.json'
        phases[editor] = dict.fromkeys(['rendered', 'line_layout', 'syntax_colors', 'selection_rendered', 'keyboard_input',
                                      'invalid_json_rejected', 'json_worker', 'json_error_marker'], True)
        phases[editor].update(content_policy_violations=[], policy_submitted=False)
        changes = [lambda values: values.clear()]
        for name in phases:
            changes.append(lambda values, name=name: values.pop(name))
        for field in ['rendered', 'line_layout', 'syntax_colors', 'selection_rendered', 'keyboard_input',
                      'invalid_json_rejected', 'json_worker', 'json_error_marker']:
            changes.append(lambda values, field=field: values[editor].update({field: False}))
        changes += [lambda v: v['dashboard-reload.json'].update(id='b' * 27),
                    lambda v: v['dashboard-reload.json'].update(verified=False),
                    lambda v: v['dashboard-verify.json'].update(session='retained-test-session'),
                    lambda v: v[editor].update(content_policy_violations=['style-src-attr']),
                    lambda v: v[editor].update(policy_submitted=True)]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            installation = self.evidence(root, dashboard=phases)
            runner.verify_application(root, installation, require_dashboard=True)
            for change in changes:
                values = copy.deepcopy(phases); change(values)
                installation = self.evidence(root, dashboard=values)
                with self.subTest(change=change), self.assertRaises(RuntimeError):
                    runner.verify_application(root, installation, require_dashboard=True)

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
