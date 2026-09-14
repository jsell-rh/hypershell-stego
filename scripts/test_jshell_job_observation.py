import subprocess
import unittest

from jshell_job_observation import StoppedJob, UnobservedJob, wait_for_result


class ObservationTests(unittest.TestCase):
    def run_observation(self, results, jobs, deadline=20):
        self.clock = 0
        self.result_reads = 0
        self.job_reads = 0
        self.notices = []
        result_values = iter(results)
        job_values = iter(jobs)

        def read(values):
            value = next(values)
            if isinstance(value, Exception):
                raise value
            return value

        def result():
            self.result_reads += 1
            return read(result_values)

        def job():
            self.job_reads += 1
            return read(job_values)

        def sleep(seconds):
            self.clock += seconds

        return wait_for_result(result, job, deadline, now=lambda: self.clock,
                               sleep=sleep, notify=self.notices.append)

    def test_result_timeout_keeps_reading_the_same_job(self):
        timeout = subprocess.TimeoutExpired("read", 30)
        self.assertEqual(self.run_observation([timeout, b"", b"0\n"], [{"status": {"active": 1}}] * 2), 0)
        self.assertEqual((self.result_reads, self.job_reads), (3, 2))
        self.assertEqual(len(self.notices), 1)

    def test_api_observation_loss_can_recover(self):
        failure = subprocess.CalledProcessError(1, "read")
        self.assertEqual(self.run_observation([failure, b"1\n"], [failure]), 1)

    def test_deadline_does_not_make_an_active_job_terminal(self):
        with self.assertRaises(UnobservedJob):
            self.run_observation([b"", b""], [{"status": {"active": 1}}] * 2, deadline=5)

    def test_unknown_state_at_deadline_requires_inspection(self):
        failure = subprocess.TimeoutExpired("read", 30)
        with self.assertRaises(UnobservedJob):
            self.run_observation([failure], [failure], deadline=0)

    def test_missing_or_terminal_job_is_not_a_pass(self):
        for job in [None, {"status": {"conditions": [{"type": "Failed", "status": "True"}]}},
                    {"status": {"conditions": [{"type": "Complete", "status": "True"}]}}]:
            with self.subTest(job=job), self.assertRaises(StoppedJob):
                self.run_observation([b""], [job])

    def test_invalid_result_keeps_the_fixture(self):
        for result in [b"unknown", b"-1", b"256"]:
            with self.subTest(result=result), self.assertRaises(UnobservedJob):
                self.run_observation([result], [])


if __name__ == "__main__":
    unittest.main()
