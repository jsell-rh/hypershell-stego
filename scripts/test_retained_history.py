"""Small parser checks. These tests do not run a benchmark."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("measurement", Path(__file__).with_name("verify-retained-history.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.log = "\n".join(
            f"BenchmarkRetainedGrantInventory/rows-{size} 3 100000 ns/op 1000 B/op 100 allocs/op {size // 100 + 1} pages/op {size + 1} rows/op 10000000 process-max-rss-B"
            for size in [10000, 100000] for _ in range(3)) + "\nPASS\n"
        self.state = {"Status": "exited", "ExitCode": 0, "Running": False,
                      "OOMKilled": False, "Dead": False, "Error": ""}
        self.limits = {"NanoCpus": 1000000000, "Memory": 768 << 20,
                       "MemorySwap": 768 << 20, "PidsLimit": 128,
                       "Privileged": False, "ReadonlyRootfs": True,
                       "NetworkMode": "host", "CapDrop": ["ALL"],
                       "SecurityOpt": ["no-new-privileges"]}

    def test_complete_results(self):
        result = module.verify(self.log, self.state, self.limits)
        self.assertEqual(len(result["samples"][100000]), 3)

    def test_incomplete_failed_or_skipped_results(self):
        for log in ["", self.log.replace("PASS", ""), self.log + "FAIL\n",
                    self.log + "SKIP\n", self.log.split("\n", 1)[1:][0]]:
            with self.subTest(log=log[:60]), self.assertRaises(ValueError):
                module.verify(log, self.state, self.limits)

    def test_invalid_or_incomplete_metrics(self):
        for log in [self.log.replace("100000 ns/op", "NaN ns/op"),
                    self.log.replace("100001 rows/op", "100000 rows/op"),
                    self.log.replace("100 allocs/op", "100 B/op"),
                    self.log.replace("10000000 process-max-rss-B", "9999999999 process-max-rss-B"),
                    self.log.replace(" 3 ", " 1 ")]:
            with self.subTest(log=log[:60]), self.assertRaises(ValueError):
                module.verify(log, self.state, self.limits)

    def test_nonterminal_failed_or_killed_container(self):
        for key, value in [("Running", True), ("Status", "running"),
                           ("ExitCode", 1), ("OOMKilled", True),
                           ("Dead", True), ("Error", "failure")]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                module.verify(self.log, {**self.state, key: value}, self.limits)

    def test_missing_or_changed_limits(self):
        for key in self.limits:
            values = dict(self.limits)
            del values[key]
            with self.subTest(key=key), self.assertRaises(ValueError):
                module.verify(self.log, self.state, values)


if __name__ == "__main__":
    unittest.main()
