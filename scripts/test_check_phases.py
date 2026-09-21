#!/usr/bin/env python3
"""Check phase records and exit codes with small command stubs."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class CheckPhases(unittest.TestCase):
    def setUp(self):
        state = Path(os.environ.get('XDG_STATE_HOME', Path.home() / '.local/state'))
        state = state / 'stego/phase-unit-checks'
        state.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=state)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        scripts = self.root / 'scripts'
        scripts.mkdir()
        for name in ['check-web-console.sh', 'check-phases.sh']:
            shutil.copyfile(Path(__file__).with_name(name), scripts / name)
        (self.root / 'bin').mkdir()
        pnpm = self.root / 'bin/pnpm'
        pnpm.write_text('''#!/usr/bin/env bash
set -eu
[[ ! -v GH_TOKEN && ! -v GITHUB_TOKEN ]]
if [[ ${FAIL_STAGE:-} == typecheck && $3 == typecheck ]]; then exit 23; fi
''')
        pnpm.chmod(0o700)
        for name, phase in [('check-console-assets.sh', 'assets'), ('generate.sh', 'regeneration')]:
            file = scripts / name
            file.write_text('''#!/usr/bin/env bash
set -eu
[[ $GH_TOKEN == private-test-value && ! -v GITHUB_TOKEN ]]
''' + f'if [[ ${{FAIL_STAGE:-}} == {phase} ]]; then exit 124; fi\n')
            file.chmod(0o700)
        self.env = dict(os.environ, GH_TOKEN='private-test-value', GITHUB_TOKEN='other-private-value',
                        STEGO_CHECK_PHASE_ROOT=str(self.root / 'public'),
                        PATH=str(self.root / 'bin') + os.pathsep + os.environ['PATH'])
        self.env.pop('FAIL_STAGE', None)

    def run_check(self, phase=''):
        result = subprocess.run(['bash', 'scripts/check-web-console.sh'], cwd=self.root,
                                env=dict(self.env, FAIL_STAGE=phase), capture_output=True, timeout=5)
        files = list((self.root / 'public').glob('web-console.*.tsv'))
        self.assertEqual(len(files), 1)
        text = files[0].read_text()
        for private in ['private-test-value', 'other-private-value', str(self.root), 'GH_TOKEN']:
            self.assertNotIn(private, text + result.stderr.decode() + result.stdout.decode())
        rows = [row.split('\t') for row in text.splitlines()]
        self.assertTrue(all(len(row) == 4 and row[0].isdigit() for row in rows))
        return result, rows

    def test_complete_check_includes_assets_and_regeneration(self):
        result, rows = self.run_check()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(rows[-1][1:], ['regeneration', 'exit', '0'])
        self.assertIn(['assets', 'complete', '0'], [row[1:] for row in rows])
        self.assertEqual(sum(row[2] == 'complete' for row in rows), 11)

    def test_asset_timeout_is_preserved_and_stops_regeneration(self):
        result, rows = self.run_check('assets')
        self.assertEqual(result.returncode, 124)
        self.assertEqual(rows[-1][1:], ['assets', 'exit', '124'])
        self.assertNotIn('regeneration', [row[1] for row in rows])

    def test_regeneration_timeout_is_preserved(self):
        result, rows = self.run_check('regeneration')
        self.assertEqual(result.returncode, 124)
        self.assertEqual(rows[-1][1:], ['regeneration', 'exit', '124'])

    def test_early_failure_is_preserved(self):
        result, rows = self.run_check('typecheck')
        self.assertEqual(result.returncode, 23)
        self.assertEqual(rows[-1][1:], ['typecheck', 'exit', '23'])
        self.assertNotIn('assets', [row[1] for row in rows])

    def test_nested_invocations_have_separate_records(self):
        script = '''set -euo pipefail
source scripts/check-phases.sh
check_phases_init compiler-setup
check_phase_start tooling-fetch
exit 124
'''
        for _ in range(2):
            result = subprocess.run(['bash', '-c', script], cwd=self.root, env=self.env,
                                    capture_output=True, timeout=5)
            self.assertEqual(result.returncode, 124)
        records = list((self.root / 'public').glob('compiler-setup.*.tsv'))
        self.assertEqual(len(records), 2)
        for record in records:
            self.assertTrue(record.read_text().endswith('\ttooling-fetch\texit\t124\n'))
            self.assertEqual(record.stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
