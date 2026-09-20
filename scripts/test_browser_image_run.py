#!/usr/bin/env python3
"""Check that the live gate rejects a different or incomplete image run."""

import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    'selection', Path(__file__).with_name('select-browser-image-run.py'))
selection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(selection)


class ImageRunTests(unittest.TestCase):
    def setUp(self):
        self.run = {
            'id': 123, 'head_sha': 'a' * 40, 'run_attempt': 1,
            'status': 'completed', 'conclusion': 'success',
            'path': '.github/workflows/application-images.yml',
            'repository': {'full_name': 'owner/repo'},
            'event': 'workflow_dispatch', 'head_branch': 'fixture',
        }
        self.policy = {
            'format': 2, 'repository': 'owner/repo', 'revision': 'a' * 40,
            'application_revision': 'a' * 40, 'reference': 'refs/heads/fixture',
        }

    def check(self, run=None, policy=None):
        return selection.validate_run(
            self.run if run is None else run, 'owner/repo', 'a' * 40, '123', '1',
            self.policy if policy is None else policy)

    def test_exact_success(self):
        self.assertEqual(self.check()['head_sha'], 'a' * 40)

    def test_different_or_incomplete_run(self):
        changes = {
            'id': 124, 'head_sha': 'b' * 40, 'run_attempt': 2,
            'status': 'in_progress', 'conclusion': 'failure',
            'path': '.github/workflows/other.yml',
            'repository': {'full_name': 'other/repo'},
            'event': 'pull_request', 'head_branch': 'other',
        }
        for key, value in changes.items():
            with self.subTest(key=key):
                run = copy.deepcopy(self.run)
                run[key] = value
                with self.assertRaises(ValueError):
                    self.check(run=run)

    def test_different_policy(self):
        for key in self.policy:
            with self.subTest(key=key):
                policy = dict(self.policy)
                policy[key] = 'different'
                with self.assertRaises(ValueError):
                    self.check(policy=policy)

    def test_bounded_unambiguous_json(self):
        for value in ['[]', '{"format":1,"format":2}', ' ' * 65537]:
            with self.subTest(value=value[:30]):
                with self.assertRaises(ValueError):
                    selection.document(value)


if __name__ == '__main__':
    unittest.main()
