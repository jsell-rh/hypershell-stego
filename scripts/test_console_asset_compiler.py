#!/usr/bin/env python3
"""Check asset compiler selection with small local stub files."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class ConsoleAssetCompiler(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'console/ui').mkdir(parents=True)
        (self.root / 'console/ui/build.zip').write_bytes(b'captured assets')
        shutil.copyfile(Path(__file__).with_name('check-console-assets.sh'),
                        self.root / 'scripts/check-console-assets.sh')
        (self.root / 'scripts/prepare-compiler.sh').write_text('''#!/bin/sh
set -eu
test "$GH_TOKEN" = test-only-auth
printf 'prepared\\n' >> "$ASSET_TEST_TRACE"
mkdir -p "$1/verified"
cp "$ASSET_TEST_COMPILER" "$1/verified/stego-linux-amd64"
''')
        self.compiler = self.root / 'compiler'
        self.compiler.write_text('''#!/usr/bin/env python3
import os,sys
from pathlib import Path
assert 'GH_TOKEN' not in os.environ and 'GITHUB_TOKEN' not in os.environ
assert sys.argv[1:4] == ['assets', '--directory', 'components/web-console/build/client']
assert sys.argv[4] == '--output'
Path(sys.argv[5]).write_bytes(b'captured assets')
''')
        self.compiler.chmod(0o700)

    def run_check(self, **overrides):
        env = dict(os.environ, GH_TOKEN='test-only-auth', GITHUB_TOKEN='test-only-auth',
                   STEGO_ASSET_RECORD_ROOT=str(self.root / 'records'),
                   ASSET_TEST_COMPILER=str(self.compiler), ASSET_TEST_TRACE=str(self.root / 'trace'))
        env.pop('STEGO_BIN', None)
        env.pop('STEGO_COMPILER_PACKAGE', None)
        env.update(overrides)
        return subprocess.run(['bash', 'scripts/check-console-assets.sh'], cwd=self.root,
                              env=env, capture_output=True, timeout=5)

    def test_installation_authentication_is_removed_before_asset_capture(self):
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual((self.root / 'trace').read_text(), 'prepared\n')
        captures = list((self.root / 'records').glob('check.*/build.zip'))
        self.assertEqual(len(captures), 1)
        self.assertEqual(captures[0].read_bytes(), b'captured assets')

    def test_different_assets_are_rejected(self):
        (self.root / 'console/ui/build.zip').write_bytes(b'old assets')
        self.assertNotEqual(self.run_check().returncode, 0)

    def test_unverified_binary_override_is_rejected_before_installation(self):
        result = self.run_check(STEGO_BIN=str(self.compiler))
        self.assertEqual(result.returncode, 2)
        self.assertIn(b'STEGO_COMPILER_PACKAGE', result.stderr)
        self.assertFalse((self.root / 'trace').exists())


if __name__ == '__main__':
    unittest.main()
