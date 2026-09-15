"""Check CI lifetime limits without network calls or real credentials."""

import base64
import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

from ci_credentials import API_SECONDS, BROWSER_SECONDS, CNPG_SECONDS, SUBJECT, require_context_credentials, require_credentials

ROOT = Path(__file__).resolve().parent.parent


def config(**claims):
    payload = {'sub': SUBJECT, 'iat': 10000, 'exp': 13600, **claims}
    encoded = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip('=')
    return {'clusters': [{'cluster': {'server': 'https://cluster.example'}}],
            'users': [{'user': {'token': 'header.' + encoded + '.signature'}}]}


class Credentials(unittest.TestCase):
    def test_job_budgets_and_collection_margin(self):
        self.assertEqual(API_SECONDS, 20 * 60 + 5 * 60)
        self.assertEqual(BROWSER_SECONDS, 30 * 60 + 5 * 60)
        self.assertEqual(CNPG_SECONDS, BROWSER_SECONDS + 10 * 60)
        for budget in (API_SECONDS, BROWSER_SECONDS, CNPG_SECONDS):
            value = config()
            require_credentials(value, budget, now=13600-budget)
            with self.assertRaisesRegex(RuntimeError, 'too little time'):
                require_credentials(value, budget, now=13601-budget)

    def test_context_read_uses_selected_file_and_keeps_failures_private(self):
        reply = subprocess.CompletedProcess([], 0, stdout=json.dumps(config()).encode())
        with mock.patch('ci_credentials.time.time', return_value=10000), mock.patch.object(subprocess, 'run', return_value=reply) as run:
            token, cluster = require_context_credentials('selected', CNPG_SECONDS, '/private/config')
            self.assertEqual(token, config()['users'][0]['user']['token'])
            self.assertEqual(cluster['server'], 'https://cluster.example')
        run.assert_called_once_with(['oc', '--context=selected', '--kubeconfig=/private/config',
                                     'config', 'view', '--raw', '--minify', '-o', 'json'],
                                    capture_output=True, check=True, timeout=30)
        for error in [FileNotFoundError('private-marker'),
                      subprocess.TimeoutExpired(['oc'], 30, output=b'private-marker'),
                      subprocess.CalledProcessError(1, ['oc'], output=b'private-marker')]:
            with mock.patch.object(subprocess, 'run', side_effect=error), self.assertRaisesRegex(RuntimeError, '^CI credential inspection failed$'):
                require_context_credentials('selected', CNPG_SECONDS)
        with mock.patch.object(subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, stdout=b'private-marker')):
            with self.assertRaisesRegex(RuntimeError, '^CI credential inspection failed$'):
                require_context_credentials('selected', CNPG_SECONDS)

    def test_cleanup_can_use_remaining_valid_time(self):
        require_credentials(config(), 0, now=13599)
        with self.assertRaises(RuntimeError):
            require_credentials(config(), 0, now=13600)

    def test_rejects_wrong_identity_and_invalid_lifetime(self):
        for claims in ({'sub': 'operator'}, {'iat': 0}, {'exp': 20000}, {'iat': 10031},
                       {'exp': True}, {'iat': '10000'}, {'exp': 10000}, {'nbf': 10031}, {'nbf': False}):
            with self.subTest(claims=claims), self.assertRaises(RuntimeError):
                require_credentials(config(**claims), 0, now=10000)

    def test_rejects_other_authentication_and_unverified_tls(self):
        values = []
        for user in ({'exec': {}}, {'token': 'private-malformed-value'}, {'token': 1},
                     {'token': 'x' * 16385}, {'token': 'header.!.signature'}):
            value = config(); value['users'][0]['user'] = user; values.append(value)
        value = config(); value['clusters'][0]['cluster']['insecure-skip-tls-verify'] = True; values.append(value)
        value = config(); value['clusters'][0]['cluster']['server'] = 'http://cluster.example'; values.append(value)
        value = config(); value['users'].append(copy.deepcopy(value['users'][0])); values.append(value)
        values.extend([{}, None, {'clusters': [], 'users': []}])
        for value in values:
            with self.subTest(), self.assertRaises(RuntimeError) as caught:
                require_credentials(value, 0, now=10000)
            self.assertNotIn('private-malformed-value', str(caught.exception))

    def test_api_refuses_stale_credentials_before_cluster_calls(self):
        spec = importlib.util.spec_from_file_location('api_gate', ROOT / 'scripts/check-jshell-gateway.py')
        runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)
        with tempfile.TemporaryDirectory() as directory:
            args = ['check', '--kubeconfig', directory + '/private', '--context', 'explicit-ci',
                    '--source', str(ROOT), '--results', directory + '/result']
            now = int(time.time())
            result = subprocess.CompletedProcess([], 0, stdout=json.dumps(config(iat=now-3000, exp=now+600)).encode())
            with mock.patch.dict(os.environ), mock.patch.object(sys, 'argv', args), mock.patch.object(subprocess, 'run', return_value=result) as run:
                with self.assertRaises(RuntimeError):
                    runner.main()
            self.assertEqual(run.call_count, 1)
            self.assertEqual(run.call_args.args[0][-6:], ['config', 'view', '--raw', '--minify', '-o', 'json'])

    def test_browser_refuses_stale_credentials_before_lease(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ['scripts', '.stego', 'acceptance', 'bin']:
                (root / name).mkdir()
            for name in ['check-service-deployment.sh', 'ci_credentials.py']:
                shutil.copyfile(ROOT / 'scripts' / name, root / 'scripts' / name)
            (root / '.stego/compiler-revision').write_text('a' * 40 + '\n')
            (root / 'acceptance/browser-inspection-source.json').write_text('{}')
            # The fake client records every call. It cannot reach a real cluster.
            client = root / 'bin/oc'
            now = int(time.time())
            client.write_text('#!' + sys.executable + '\nimport json,sys\nfrom pathlib import Path\n'
                              'p=Path(' + repr(str(root / 'calls')) + ')\n'
                              'with p.open("a") as out: out.write(json.dumps(sys.argv[1:])+"\\n")\n'
                              'print(' + repr(json.dumps(config(iat=now-3000, exp=now+600))) + ')\n')
            client.chmod(0o700)
            environment = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'],
                               STEGO_TEST_CONTEXT='explicit-ci', STEGO_TEST_PREINSTALLED='1',
                               STEGO_TEST_BROWSER_WORKLOAD='1', STEGO_TEST_BROWSER_DEPLOYMENT='1',
                               STEGO_TEST_GATEWAY_INTERNAL_CA_FILE=str(root / 'not-read-ca.pem'),
                               STEGO_TEST_GATEWAY_CLUSTER_ISSUER='test', STEGO_TEST_RESULTS=str(root / 'result'))
            result = subprocess.run(['bash', str(root / 'scripts/check-service-deployment.sh')],
                                    env=environment, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()]
            self.assertEqual(calls, [['--context=explicit-ci', 'config', 'view', '--raw', '--minify', '-o', 'json']])
            self.assertIn(b'too little time', result.stderr)
            self.assertNotIn(b'header.', result.stderr)

    def test_cnpg_refuses_short_credentials_before_installation(self):
        spec = importlib.util.spec_from_file_location('cnpg_gate', ROOT / 'scripts/check-cnpg-installation.py')
        runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            private = root / 'private'
            private.write_text('private fixture')
            private.chmod(0o600)
            args = ['check', '--operator-context', 'explicit-operator', '--ci-kubeconfig', str(private),
                    '--source', str(ROOT), '--repository', str(ROOT), '--results', str(root / 'result')]
            now = int(time.time())
            # Enough time for the browser alone, but not for CNPG installation.
            value = config(iat=now-1000, exp=now+2600)
            result = subprocess.CompletedProcess([], 0, stdout=json.dumps(value).encode())
            record = {'cnpg_installation': {'namespace': 'stego-cnpg-database-test'}}
            with mock.patch.object(sys, 'argv', args), mock.patch.object(runner, 'verify_source', return_value=record), \
                 mock.patch.object(runner, 'module', side_effect=AssertionError('Installation started before the credential check')) as modules, \
                 mock.patch.object(subprocess, 'run', return_value=result) as run:
                with self.assertRaisesRegex(RuntimeError, 'too little time'):
                    runner.main()
            modules.assert_not_called()
            self.assertFalse((root / 'result').exists())
            self.assertEqual(run.call_count, 1)
            command = run.call_args.args[0]
            self.assertIn('--context=jshell-ci', command)
            self.assertIn('--kubeconfig=' + str(private), command)
            self.assertEqual(command[-6:], ['config', 'view', '--raw', '--minify', '-o', 'json'])


if __name__ == '__main__':
    unittest.main()
