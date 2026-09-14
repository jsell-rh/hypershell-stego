"""Read a result from the same Job without treating transport loss as completion."""

import subprocess
import time


class UnobservedJob(RuntimeError):
    """Keep the Job, its fixture, and its Lease for inspection."""


class StoppedJob(RuntimeError):
    """The API confirmed that the Job is absent or terminal."""


def wait_for_result(read_result, read_job, deadline, *, now=time.monotonic,
                    sleep=time.sleep, notify=print):
    warned = False
    while True:
        try:
            raw = read_result()
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
            raw = b""
            if not warned:
                notify("Result observation failed; read the same Job again.")
                warned = True
        if raw.strip():
            try:
                code = int(raw.strip())
            except ValueError:
                raise UnobservedJob("The Job result is invalid") from None
            if code < 0 or code > 255:
                raise UnobservedJob("The Job result is invalid")
            return code
        try:
            job = read_job()
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
            job = "unobserved"
        if job is None:
            raise StoppedJob("The Job is absent and has no saved result")
        if isinstance(job, dict) and any(
            item.get("status") == "True" and item.get("type") in {"Failed", "Complete"}
            for item in job.get("status", {}).get("conditions", [])
        ):
            raise StoppedJob("The Job is terminal and has no saved result")
        if now() >= deadline:
            raise UnobservedJob("The result deadline expired; retain the Job and Lease")
        sleep(5)
