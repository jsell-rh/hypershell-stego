#!/usr/bin/env python3
"""Run the Gateway API gate with the restricted jshell CI identity.

The operator creates the CI namespace and permissions. This runner creates only
one bounded Job and its private database fixture. It never uses kind.
"""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import tarfile
import tempfile
import time
import uuid
from ci_credentials import API_SECONDS, require_credentials

REQUIRED = [
    "TestServiceAccountProviderStateAcrossLockAndRestart",
    "TestGatewayProviderStateAcrossGRPCAndRestart",
    "TestGatewayDatabasePoolWaitCancellationAndRestart",
    "TestConcurrentGlobalRoleProjection",
    "TestCleanupSummaryScopeAndTimestampChecks",
    "TestGatewayDatabaseSecretFileAndCredentialRotation",
    "TestGatewayDeletionBeforeWorkloadStartup",
    "TestControllerLocalBootstrapRejectsUnregisteredSQLGeneration",
    "TestGeneratedWorkloadWorkerStartupPrivacy",
    "TestGatewaySQLUsesDurableStateAndRetainsSuppliedServer",
    "TestGatewayCreationCommitsOwnerAndEvent",
    "TestGatewayRequestsRejectRetiredDatabaseField",
    "TestGeneratedGoSDKGatewayWorkflow",
    "TestGeneratedCLIWorkflow",
    "TestGatewayMutationWorkflowAcrossTransportsAndRestart",
    "TestGatewayControllerWriteGrantsAcrossPlacementAndRestart",
    "TestOwnerGrantFailureRollsBackGatewayAndEvent",
    "TestEventFailureRollsBackGatewayAndOwner",
    "TestAccessFiltersRunBeforeCountAndPagination",
    "TestGeneratedGatewayDescriptorsMatchReleaseContract",
    "TestGatewayWorkflowThroughGeneratedRESTProcess",
    "TestGatewayWorkflowAcrossRESTAndGRPC",
    "TestGeneratedRuntimeDeliversGatewayEventsAcrossRestart",
    "TestGatewayWatchThroughGeneratedRuntime",
    "TestPlacementWorkflowThroughGeneratedRuntime",
    "TestControllerLocalSchemaHasNoDatabaseCatalog",
    "TestControllerLocalSQLCleanupRequiresExactGrantAndVersion",
    "TestControllerLocalBootstrapRejectsLegacySchemaWithoutWrites",
    "TestGatewaySQLCleanupObservationIsAtomicAndSurvivesRestart",
    "TestClusterDeletionWaitsForGatewaySQLAndWorkloadCleanupAcrossRestart",
    "TestGatewaySQLCleanupMakesIndependentProgressAfterRestart",
    "TestGatewaySQLCleanupDeadlineKeepsStateUntilRecovery",
    "TestGeneratedCLICatalogWorkflow",
    "TestGeneratedCLIApplyWorkflow",
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True, type=Path)
    parser.add_argument("--context", required=True)
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--results", required=True, type=Path)
    args = parser.parse_args()
    root, result = args.source.resolve(), args.results.resolve()
    result.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.environ["KUBECONFIG"] = str(args.kubeconfig.resolve())
    spec = importlib.util.spec_from_file_location("live_lock", root / "scripts/jshell_live_lock.py")
    lock = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(lock)
    observer_spec = importlib.util.spec_from_file_location("job_observation", root / "scripts/jshell_job_observation.py")
    observer = importlib.util.module_from_spec(observer_spec)
    observer_spec.loader.exec_module(observer)
    namespace, name = "stego-ci", "gateway-api-" + uuid.uuid4().hex[:12]
    prefix = ["oc", "--context=" + args.context, "--request-timeout=20s", "-n", namespace]

    def oc(*words, data=None, timeout=30):
        # Do not print command output. API responses can contain test Secrets.
        return subprocess.run(prefix + list(words), input=data, capture_output=True, check=True, timeout=timeout).stdout

    def get(kind, resource):
        raw = oc("get", kind, resource, "--ignore-not-found", "-o", "json")
        return json.loads(raw) if raw.strip() else None

    config = json.loads(oc('config', 'view', '--raw', '--minify', '-o', 'json'))
    require_credentials(config, API_SECONDS)

    untracked = subprocess.check_output(["git", "ls-files", "--others", "--exclude-standard", "-z"], cwd=root).decode().split("\0")
    if any(path.endswith(".go") for path in untracked):
        raise RuntimeError("Track Go source files before freezing test input")
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    if "scripts/check-jshell-gateway.py" not in names:
        raise RuntimeError("Track the runner before freezing source")
    hashes = {}
    with tarfile.open(result / "source.tar", "w") as archive:
        for path in sorted(set(names)):
            if path and (root / path).is_file():
                hashes[path] = hashlib.sha256((root / path).read_bytes()).hexdigest()
                archive.add(root / path, arcname=path, recursive=False)
    (result / "source-sha256.json").write_text(json.dumps(hashes, indent=2) + "\n")
    (result / "required-tests.json").write_text(json.dumps(REQUIRED, indent=2) + "\n")
    base = json.loads((root / "acceptance/kubernetes-service-job.json").read_text())
    pod = next(item for item in base["items"] if item["kind"] == "Job")["spec"]["template"]["spec"]
    pg, test = pod["initContainers"][0], pod["containers"][0]
    test["env"] = [item for item in test["env"] if item["name"] not in {
        "STEGO_TEST_KUBERNETES_SERVICE", "STEGO_TEST_NAMESPACE", "STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK",
    }]
    test["env"].append({"name": "GOWORK", "value": "off"})
    test["resources"]["limits"]["ephemeral-storage"] = "6Gi"
    run = r'''mkdir -p /work/tmp /work/application
while [ ! -f /work/start ]; do sleep 1; done
cd /work/application
(
set -e
snapshot() {
  find out console/out -type f | sort > /work/current-files
  printf '%s\n' .stego/state.yaml console/.stego/state.yaml go.mod go.sum console/go.mod console/go.sum >> /work/current-files
  if [ -f /work/generated-files ]; then cmp /work/generated-files /work/current-files; else cp /work/current-files /work/generated-files; fi
  xargs sha256sum < /work/generated-files > "/work/$1.sha256"
}
snapshot committed
go mod verify
STEGO_REQUIRE_GATEWAY_SQL=1 go test -json -race -mod=readonly -count=1 -timeout=3m ./internal/gatewayworkload -run '^TestGatewaySQLUsesDurableStateAndRetainsSuppliedServer$' > /work/tests.jsonl
go test -json -race -mod=readonly -count=1 -timeout=3m ./internal/cleanupmetrics >> /work/tests.jsonl
bash scripts/generate.sh
snapshot first
cmp /work/committed.sha256 /work/first.sha256
bash scripts/generate.sh
snapshot second
cmp /work/first.sha256 /work/second.sha256
tar cf /work/generated.tar -T /work/generated-files
go test -json -race -mod=readonly -count=1 -timeout=12m ./acceptance -run '@TESTS@' >> /work/tests.jsonl
snapshot after
cmp /work/first.sha256 /work/after.sha256
) > /work/test.log 2>&1
code=$?
echo "$code" > /work/result.tmp
mv /work/result.tmp /work/result
while [ ! -f /work/collected ]; do sleep 1; done
exit "$code"
'''.replace("@TESTS@", "^(" + "|".join(REQUIRED) + ")$")
    test["command"] = ["sh", "-c", run]
    label = {"stego.test/run": name}
    job = {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": name, "namespace": namespace, "labels": label}, "spec": {
        "backoffLimit": 0, "activeDeadlineSeconds": 1200, "ttlSecondsAfterFinished": 3600,
        "template": {"metadata": {"labels": label}, "spec": {
            "automountServiceAccountToken": False, "restartPolicy": "Never",
            "securityContext": {"runAsNonRoot": True, "seccompProfile": {"type": "RuntimeDefault"}},
            "initContainers": [pg], "containers": [test], "volumes": pod["volumes"],
            "nodeSelector": {"kubernetes.io/os": "linux", "kubernetes.io/arch": "amd64"},
        }}}}
    (result / "job.json").write_text(json.dumps(job, indent=2) + "\n")
    targets = [("Secret", "cli-test-postgres"), ("Secret", "database-tls"), ("ConfigMap", "database-ca"), ("Job", name)]
    pod_name, code = None, None
    started, collected, stopped = False, False, False
    print("Gateway API Job: " + namespace + "/" + name, flush=True)
    lock.acquire(args.context, name, namespace, name)
    try:
        if json.loads(oc("get", "jobs", "-o", "json"))["items"]:
            raise RuntimeError("Inspect existing CI Jobs before a new run")
        for kind, resource in targets:
            if get(kind, resource):
                raise RuntimeError("A CI fixture resource already exists; inspect it first")
        with tempfile.TemporaryDirectory(prefix="gateway-api-private-") as private:
            key, cert = Path(private) / "server.key", Path(private) / "server.crt"
            subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-keyout", str(key), "-out", str(cert), "-days", "2", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
            password = secrets.token_urlsafe(32)
            dsn = "postgres://postgres:" + password + "@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt"
            values = [
                {"stringData": {"password": password, "dsn": dsn, "url": dsn}, "type": "Opaque"},
                {"stringData": {"server.key": key.read_text(), "server.crt": cert.read_text()}, "type": "Opaque"},
                {"data": {"server.crt": cert.read_text()}},
            ]
            for (kind, resource), value in zip(targets, values):
                obj = {"apiVersion": "v1", "kind": kind, "metadata": {"name": resource, "namespace": namespace, "labels": label}, **value}
                oc("create", "-f", "-", data=json.dumps(obj).encode())
        oc("create", "-f", "-", data=json.dumps(job).encode())
        deadline = time.monotonic() + 1200
        while not pod_name:
            pods = json.loads(oc("get", "pods", "-l", "job-name=" + name, "-o", "json"))["items"]
            if len(pods) == 1:
                pod_name = pods[0]["metadata"]["name"]
                break
            current = get("Job", name)
            if not current or any(c["type"] == "Failed" and c["status"] == "True" for c in current.get("status", {}).get("conditions", [])) or time.monotonic() > deadline:
                raise RuntimeError("The test Job did not start")
            time.sleep(2)
        oc("wait", "--for=condition=Ready", "pod/" + pod_name, "--timeout=150s", timeout=170)
        oc("exec", "-i", pod_name, "-c", "test", "--", "tar", "xf", "-", "-C", "/work/application", data=(result / "source.tar").read_bytes(), timeout=120)
        # The start write can succeed even if its response is lost.
        started = True
        oc("exec", pod_name, "-c", "test", "--", "touch", "/work/start")
        try:
            code = observer.wait_for_result(
                lambda: oc("exec", pod_name, "-c", "test", "--", "sh", "-c", 'if [ -f /work/result ]; then cat /work/result; fi'),
                lambda: get("Job", name), deadline)
        except observer.StoppedJob:
            stopped = True
            raise
        for path in ["test.log", "tests.jsonl", "committed.sha256", "first.sha256", "second.sha256", "after.sha256", "generated.tar"]:
            data = oc("exec", pod_name, "-c", "test", "--", "sh", "-c", 'if [ -f /work/"$1" ]; then cat /work/"$1"; fi', "collect", path, timeout=90)
            (result / path).write_bytes(data)
        collected = True
        oc("exec", pod_name, "-c", "test", "--", "touch", "/work/collected")
        oc("wait", "--for=condition=" + ("Complete" if code == 0 else "Failed"), "job/" + name, "--timeout=60s", timeout=80)
        (result / "job-final.json").write_text(json.dumps(get("Job", name), indent=2) + "\n")
        events = [json.loads(line) for line in (result / "tests.jsonl").read_text().splitlines()]
        passed = {event["Test"] for event in events if event.get("Action") == "pass" and "Test" in event}
        failed = [event.get("Test", event.get("Package")) for event in events if event.get("Action") == "fail"]
        missing = sorted(set(REQUIRED) - passed)
        proof = {"required": REQUIRED, "passed": sorted(passed), "failed": failed, "missing": missing, "exit_code": code}
        (result / "verification.json").write_text(json.dumps(proof, indent=2) + "\n")
        print(json.dumps(proof), flush=True)
        if code != 0 or missing or failed:
            raise RuntimeError("The Gateway API gate failed; inspect the saved results")
    finally:
        if started and not collected and not stopped:
            (result / "retained.json").write_text(json.dumps({"job": name, "namespace": namespace, "reason": "result_not_collected", "lease_retained": True}) + "\n")
            print("The Job result is not collected. Retain its fixture and Lease for inspection.", flush=True)
        else:
            # Keep the Lease if cleanup or an ownership check fails.
            for kind, resource in reversed(targets):
                current = get(kind, resource)
                if not current:
                    continue
                meta = current["metadata"]
                if meta.get("labels", {}).get("stego.test/run") != name:
                    raise RuntimeError("CI cleanup refuses a resource with another owner")
                if kind == "Job":
                    (result / "job-final.json").write_text(json.dumps(current, indent=2) + "\n")
                group = "/apis/batch/v1" if kind == "Job" else "/api/v1"
                path = group + "/namespaces/" + namespace + "/" + {"Job": "jobs", "Secret": "secrets", "ConfigMap": "configmaps"}[kind] + "/" + resource
                options = {"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": {"uid": meta["uid"], "resourceVersion": meta["resourceVersion"]}, "propagationPolicy": "Foreground"}
                oc("delete", "--raw=" + path, "-f", "-", data=json.dumps(options).encode())
                until = time.monotonic() + 90
                while get(kind, resource):
                    if time.monotonic() >= until:
                        raise RuntimeError("A CI fixture resource remains; retain the shared Lease")
                    time.sleep(1)
            if json.loads(oc("get", "pods", "-l", "job-name=" + name, "-o", "json"))["items"]:
                raise RuntimeError("CI test Pods remain; retain the shared Lease")
            (result / "cleanup.json").write_text(json.dumps({"job_absent": name, "pods_absent": True, "fixture_resources_absent": True}) + "\n")
            lock.release(args.context, name)
    print("Gateway API gate passed. Results: " + str(result), flush=True)


if __name__ == "__main__":
    main()
