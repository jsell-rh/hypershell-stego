"""Read a result from the same Job without treating transport loss as completion."""

import subprocess
import time


class UnobservedJob(RuntimeError):
    """Keep the Job, its fixture, and its Lease for inspection."""


class StoppedJob(RuntimeError):
    """The API confirmed that the Job is absent or terminal."""


def wait_for_completion(acknowledge, read_job, expected_uid, deadline, *,
                        now=time.monotonic, sleep=time.sleep, notify=print):
    """Acknowledge saved evidence, then confirm the same Job is terminal.

    A lost acknowledgement response does not show whether its write succeeded.
    Only this idempotent write can repeat. Never start the test again.
    """
    if not isinstance(expected_uid, str) or not expected_uid:
        raise ValueError("A Job UID is required")
    acknowledged = False
    warned = False
    while True:
        try:
            job = read_job()
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
            job = "unobserved"
        if job is None:
            raise UnobservedJob("The Job disappeared before completion was verified")
        if isinstance(job, dict):
            if job.get("metadata", {}).get("uid") != expected_uid:
                raise UnobservedJob("The Job identity changed; retain the fixture and Lease")
            if any(item.get("status") == "True" and item.get("type") in {"Failed", "Complete"}
                   for item in job.get("status", {}).get("conditions", [])):
                return job
        if now() >= deadline:
            raise UnobservedJob("Completion is not verified; retain the Job and Lease")
        # Verify identity before each attempt. A successful write needs no retry.
        if isinstance(job, dict) and not acknowledged:
            try:
                acknowledge()
                acknowledged = True
            except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
                if not warned:
                    notify("Collection acknowledgement failed; observe the same Job again.")
                    warned = True
        sleep(2)


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
