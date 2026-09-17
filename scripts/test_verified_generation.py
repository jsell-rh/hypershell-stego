#!/usr/bin/env python3
"""Check compiler selection with small stub files and no network or Go builds."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class VerifiedGeneration(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.project = self.root / "project"
        (self.project / "scripts").mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name("generate.sh"), self.project / "scripts/generate.sh")
        (self.project / "scripts/check-gateway-console-inputs.py").write_text("pass\n")
        self.revision = "a" * 40
        for module in [".", "console", "gateway-console"]:
            directory = self.project / module / ".stego"
            directory.mkdir(parents=True)
            (directory / "compiler-revision").write_text(self.revision + "\n")
            (directory / "state.yaml").write_text("stable state\n")
        self.compiler = self.root / "compiler"
        self.trace = self.root / "trace"
        self.compiler.write_text("""#!/bin/sh
set -eu
test -z "${GH_TOKEN:-}${GITHUB_TOKEN:-}"
printf '%s %s\\n' "$PWD" "$*" >> "$GENERATION_TEST_TRACE"
case "$1" in
  version) printf '%s\\n' '{"build":{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source_state":"clean"}}' ;;
  apply|deps|drift) : ;;
  *) exit 41 ;;
esac
""")
        self.compiler.chmod(0o700)
        self.pin()

    def pin(self):
        (self.project / ".stego/compiler-sha256").write_text(
            hashlib.sha256(self.compiler.read_bytes()).hexdigest() + "\n")

    def run_generation(self, *args):
        environment = dict(os.environ, STEGO_VERIFIED_COMPILER=str(self.compiler),
                           STEGO_GENERATION_ROOT=str(self.root / "records"),
                           GENERATION_TEST_TRACE=str(self.trace), GH_TOKEN="test-only-auth",
                           GITHUB_TOKEN="test-only-auth")
        return subprocess.run(["bash", str(self.project / "scripts/generate.sh"), *args],
                              cwd=self.project, env=environment, capture_output=True, timeout=10)

    def test_changed_compiler_is_rejected_before_execution(self):
        self.compiler.write_text(self.compiler.read_text() + "# changed\n")
        result = self.run_generation()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"Compiler bytes differ", result.stderr)
        self.assertFalse(self.trace.exists())

    def test_selected_digest_does_not_override_the_source_revision(self):
        self.compiler.write_text(self.compiler.read_text().replace("a" * 40, "b" * 40))
        self.pin()
        result = self.run_generation()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"compiler identity differs", result.stderr)
        self.assertEqual(len(self.trace.read_text().splitlines()), 1)
        self.assertTrue(self.trace.read_text().endswith("version --json\n"))

    def test_all_modules_use_the_selected_bytes_without_authentication_environment(self):
        result = self.run_generation()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        records = list((self.root / "records").glob("generation.*"))
        self.assertEqual(len(records), 1)
        self.assertEqual((records[0] / "stego").read_bytes(), self.compiler.read_bytes())
        version = json.loads((records[0] / "compiler-version.json").read_text())
        self.assertEqual(version["build"]["revision"], self.revision)
        lines = self.trace.read_text().splitlines()
        for module in [".", "console", "gateway-console"]:
            prefix = str((self.project / module).resolve()) + " "
            commands = [line[len(prefix):] for line in lines if line.startswith(prefix) and "version" not in line]
            expected = ["apply", "deps", "apply", "drift"]
            if module == "gateway-console":
                expected.insert(3, "apply")
            self.assertEqual(commands, expected)

    def test_other_module_revision_is_rejected_before_execution(self):
        (self.project / "gateway-console/.stego/compiler-revision").write_text("b" * 40 + "\n")
        self.assertNotEqual(self.run_generation().returncode, 0)
        self.assertFalse(self.trace.exists())

    def test_symbolic_link_is_rejected_before_execution(self):
        original = self.root / "original"
        self.compiler.rename(original)
        self.compiler.symlink_to(original)
        self.assertNotEqual(self.run_generation().returncode, 0)
        self.assertFalse(self.trace.exists())


if __name__ == "__main__":
    unittest.main()
