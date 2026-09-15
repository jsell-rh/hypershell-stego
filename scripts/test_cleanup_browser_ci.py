#!/usr/bin/env python3
"""Check cleanup reads, bounded retry, and retained failure state."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SOURCE = (Path(__file__).parent / 'cleanup-browser-ci.sh').read_text().split("<<'CI_CLEANUP'\n", 1)[1].split('\nCI_CLEANUP', 1)[0]


class CleanupReads(unittest.TestCase):
    def run_check(self, directory, responses):
        (directory / 'allocation-cleanup.json').write_text('{"allocations_absent": true}')
        def reply(command, **options):
            self.assertIn('--context=jshell-ci', command)
            self.assertEqual(options['timeout'], 30)
            value = next(responses)
            if isinstance(value, Exception):
                raise value
            return subprocess.CompletedProcess(command, 0, json.dumps(value).encode())
        with patch.object(sys, 'argv', ['cleanup', 'jshell-ci', 'stego-service-ci', str(directory)]), patch('subprocess.run', side_effect=reply) as calls, patch('time.sleep') as sleeps:
            exec(compile(SOURCE, 'cleanup-browser-ci.sh', 'exec'), {})
            return calls.call_count, sleeps.call_count

    def test_single_failed_read_retries_then_verifies_every_resource(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            error = subprocess.CalledProcessError(1, ['oc'])
            self.assertEqual(self.run_check(root, iter([error] + [{'items': []}] * 7)), (8, 1))
            self.assertEqual(json.loads((root / 'cleanup.json').read_text()), {'namespace_retained': 'stego-service-ci', 'test_resources_absent': True, 'allocations_absent': True})

    def test_repeated_read_failure_never_writes_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'three attempts; keep the Lease: pods'):
                self.run_check(root, iter([subprocess.TimeoutExpired(['oc'], 30)] * 3))
            self.assertFalse((root / 'cleanup.json').exists())

    def test_invalid_response_never_means_absence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'three attempts'):
                self.run_check(root, iter([{}, {'items': None}, []]))
            self.assertFalse((root / 'cleanup.json').exists())

    def test_remaining_object_never_writes_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'resources remain; keep the Lease'):
                self.run_check(root, iter([{'items': [{'metadata': {'name': 'service-check'}}]}]))
            self.assertFalse((root / 'cleanup.json').exists())


if __name__ == '__main__':
    unittest.main()
