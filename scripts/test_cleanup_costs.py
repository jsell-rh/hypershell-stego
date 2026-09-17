"""Small result checks. These tests do not execute cleanup or benchmarks."""
import importlib.util
from pathlib import Path
import unittest


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


shared = load("shared", "test_retained_history.py")
cleanup = load("cleanup", "verify-cleanup-costs.py")


class CleanupVerificationTests(unittest.TestCase):
    def setUp(self):
        fixture = shared.VerificationTests()
        fixture.setUp()
        self.state, self.limits = fixture.state, fixture.limits
        self.log = ("BenchmarkGatewayAccountJournalCleanup 1 100000 ns/op 1000 B/op 100 allocs/op 1000 accounts/op 2000 journals/op 2000 provider-deletes/op 30 cycles/op 10000000 process-max-rss-B\n" * 3) + "PASS\n"

    def test_complete_cleanup(self):
        self.assertEqual(len(cleanup.verify(self.log, self.state, self.limits)["samples"]), 3)

    def test_missing_incomplete_or_unbounded_cleanup(self):
        for log in [self.log.split("\n", 1)[1], self.log.replace("2000 provider-deletes/op", "1999 provider-deletes/op"),
                    self.log.replace("30 cycles/op", "41 cycles/op"), self.log.replace("100000 ns/op", "NaN ns/op"),
                    self.log.replace(" 1 ", " 2 "), self.log + "FAIL\n"]:
            with self.subTest(log=log[:60]), self.assertRaises(ValueError):
                cleanup.verify(log, self.state, self.limits)

    def test_shared_bounds_are_required(self):
        for key, value in [("Memory", 0), ("Privileged", True), ("PidsLimit", 0)]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                cleanup.verify(self.log, self.state, {**self.limits, key: value})

    def test_nonterminal_or_killed_container(self):
        for key, value in [("Running", True), ("OOMKilled", True), ("ExitCode", 1)]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                cleanup.verify(self.log, {**self.state, key: value}, self.limits)


if __name__ == "__main__":
    unittest.main()
