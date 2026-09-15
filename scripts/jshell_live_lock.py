"""Hold the shared live-test Lease until the test Job and its resources are gone.

An expired Lease is not permission to start another test. Inspect the old Job,
complete cleanup, then release its Lease. All calls use an explicit context.
"""

import argparse
from datetime import datetime, timezone
import json
import re
import subprocess


def oc(context, *args):
    result = subprocess.run(
        ["oc", "--context=" + context, "--request-timeout=20s", *args],
        capture_output=True, text=True, check=True, timeout=30,
    )
    return json.loads(result.stdout) if result.stdout.strip() else None


def lease(context):
    return oc(context, "-n", "stego-ci", "get", "lease", "jshell-live-test", "-o", "json")


def acquire(context, holder, namespace, job):
    for value in [holder, namespace, job]:
        if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", value):
            raise RuntimeError("Invalid live-test identity")
    current = lease(context)
    if current.get("spec", {}).get("holderIdentity"):
        raise RuntimeError("A live test holds the Lease. Inspect it before another run.")
    now = datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    annotations = dict(current["metadata"].get("annotations", {}))
    annotations.update({"stego.test/namespace": namespace, "stego.test/job": job})
    patch = [
        {"op": "test", "path": "/metadata/uid", "value": current["metadata"]["uid"]},
        {"op": "test", "path": "/metadata/resourceVersion", "value": current["metadata"]["resourceVersion"]},
        {"op": "add", "path": "/metadata/annotations", "value": annotations},
        {"op": "replace", "path": "/spec", "value": {"holderIdentity": holder, "leaseDurationSeconds": 1200, "acquireTime": now, "renewTime": now}},
    ]
    oc(context, "-n", "stego-ci", "patch", "lease", "jshell-live-test", "--type=json", "-p", json.dumps(patch), "-o", "json")


def verify(context, holder, uid, namespace, job):
    if not holder or not uid or not namespace or not job:
        raise RuntimeError("Require the held Lease identity and test target")
    current = lease(context)
    annotations = current['metadata'].get('annotations', {})
    if current['metadata']['uid'] != uid or current.get('spec', {}).get('holderIdentity') != holder or annotations.get('stego.test/namespace') != namespace or annotations.get('stego.test/job') != job:
        raise RuntimeError("The held Lease identity or test target differs")
    if oc(context, '-n', namespace, 'get', 'job', job, '--ignore-not-found', '-o', 'json'):
        raise RuntimeError("The held Lease already has a test Job; inspect it first")


def release(context, holder):
    current = lease(context)
    if current.get("spec", {}).get("holderIdentity") != holder:
        raise RuntimeError("The live-test Lease has a different holder")
    annotations = current["metadata"]["annotations"]
    if oc(context, "-n", annotations["stego.test/namespace"], "get", "job", annotations["stego.test/job"], "--ignore-not-found", "-o", "json"):
        raise RuntimeError("The old test Job still exists. Complete cleanup first.")
    patch = [
        {"op": "test", "path": "/metadata/uid", "value": current["metadata"]["uid"]},
        {"op": "test", "path": "/metadata/resourceVersion", "value": current["metadata"]["resourceVersion"]},
        {"op": "replace", "path": "/spec/holderIdentity", "value": ""},
    ]
    oc(context, "-n", "stego-ci", "patch", "lease", "jshell-live-test", "--type=json", "-p", json.dumps(patch), "-o", "json")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["acquire", "release", "verify"])
    parser.add_argument("--context", required=True)
    parser.add_argument("--holder", required=True)
    parser.add_argument("--namespace")
    parser.add_argument("--uid")
    parser.add_argument("--job", default="check")
    args = parser.parse_args()
    if args.action == "acquire":
        if not args.namespace:
            parser.error("acquire requires --namespace")
        acquire(args.context, args.holder, args.namespace, args.job)
    elif args.action == "verify":
        verify(args.context, args.holder, args.uid, args.namespace, args.job)
    else:
        release(args.context, args.holder)
