#!/usr/bin/env python3
"""Check namespace counts in an isolated, bounded OpenShift Job.

Requires an explicit context. Does not change the active context. The result
directory holds frozen source, generated files, and logs, but no credentials.
"""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import tarfile
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    root = args.source.resolve()
    spec = importlib.util.spec_from_file_location("stego_live_lock", root / "scripts/jshell_live_lock.py")
    live_lock = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(live_lock)
    result = Path(tempfile.mkdtemp(prefix="hypershell-count-live-"))
    namespace = "stego-count-live-" + uuid.uuid4().hex[:8]
    marker = hashlib.sha256((namespace + ".hypershell-namespace-allocation").encode()).hexdigest()[:32]
    (result / "namespace").write_text(namespace + "\n")
    print(f"Results: {result}\nNamespace: {namespace}", flush=True)

    def oc(*words, data=None, timeout=45):
        command = ["oc", "--context=" + args.context, "--request-timeout=30s", "-n", namespace, *words]
        return subprocess.run(command, input=data, capture_output=True, check=True, timeout=timeout).stdout

    def apply(document):
        # Do not include a Secret body in an exception or log.
        try:
            return oc("apply", "-f", "-", data=json.dumps(document).encode())
        except subprocess.CalledProcessError:
            raise RuntimeError("Kubernetes apply failed; inspect the named test resources") from None

    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    required = {"acceptance/count_namespace_live_test.go", "scripts/check-count-namespaces.py"}
    required.add("scripts/jshell_live_lock.py")
    if not required.issubset(names):
        raise RuntimeError("Track the live test and runner before freezing source")
    hashes = {}
    with tarfile.open(result / "source.tar", "w") as archive:
        for name in sorted(set(names)):
            if name and (root / name).is_file():
                hashes[name] = hashlib.sha256((root / name).read_bytes()).hexdigest()
                archive.add(root / name, arcname=name, recursive=False)
    (result / "source-sha256.json").write_text(json.dumps(hashes, indent=2) + "\n")
    base = json.loads((root / "acceptance/kubernetes-service-job.json").read_text().replace("@NAMESPACE@", namespace))
    spec = next(item for item in base["items"] if item["kind"] == "Job")["spec"]["template"]["spec"]
    pg, test, volumes = spec["initContainers"][0], spec["containers"][0], spec["volumes"]
    run = r'''mkdir -p /work/tmp /work/application
while [ ! -f /work/start ]; do sleep 1; done
cd /work/application
(
set -e
bash scripts/generate.sh
find out console/out -type f | sort > /work/generated-files
printf '%s\n' .stego/state.yaml console/.stego/state.yaml go.mod go.sum console/go.mod console/go.sum >> /work/generated-files
xargs sha256sum < /work/generated-files > /work/first.sha256
bash scripts/generate.sh
xargs sha256sum < /work/generated-files > /work/second.sha256
cmp /work/first.sha256 /work/second.sha256
tar cf /work/generated.tar out console/out .stego/state.yaml console/.stego/state.yaml go.mod go.sum console/go.mod console/go.sum
go build -o /work/render ./out/deploy/render
for worker in namespace-allocation gateway-workload sandbox-count; do
  /work/render --namespace "$STEGO_TEST_NAMESPACE" --image "$STEGO_TEST_IDLE_IMAGE" --worker "$worker" --egress "kubernetes=$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT" > "/work/$worker.json"
done
touch /work/rbac-ready
while [ ! -f /work/live-ready ]; do sleep 1; done
while [ ! -s /count-credentials/count ] || [ ! -s /count-credentials/allocator ] || [ ! -s /count-credentials/workload ]; do sleep 1; done
go test -v -race -count=1 -timeout=10m ./acceptance -run '^TestNamespaceCountWithLiveKubernetes$'
xargs sha256sum < /work/generated-files > /work/after.sha256
cmp /work/first.sha256 /work/after.sha256
) > /work/test.log 2>&1
code=$?
echo "$code" > /work/result
while [ ! -f /work/collected ]; do sleep 1; done
exit "$code"
'''
    test["command"] = ["sh", "-c", run]
    test["env"] = [x for x in test["env"] if x["name"] not in ["STEGO_TEST_KUBERNETES_SERVICE", "STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK"]]
    test["env"] += [{"name": key, "value": value} for key, value in {
        "GOWORK": "off", "STEGO_TEST_NAMESPACE_COUNT_LIVE": "1",
        "STEGO_TEST_KUBERNETES_URL": "https://kubernetes.default.svc",
        "STEGO_TEST_IDLE_IMAGE": test["image"],
    }.items()]
    test["resources"]["limits"]["ephemeral-storage"] = "6Gi"
    test["volumeMounts"].append({"name": "count-credentials", "mountPath": "/count-credentials", "readOnly": True})
    volumes.append({"name": "count-credentials", "secret": {"secretName": "count-credentials", "defaultMode": 416}})
    job = {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": "check", "namespace": namespace}, "spec": {
        "backoffLimit": 0, "activeDeadlineSeconds": 1200, "template": {"spec": {
            "restartPolicy": "Never", "automountServiceAccountToken": False,
            "securityContext": {"runAsNonRoot": True, "seccompProfile": {"type": "RuntimeDefault"}},
            "initContainers": [pg], "containers": [test], "volumes": volumes}}}}
    (result / "job.json").write_text(json.dumps(job, indent=2) + "\n")
    def obj(kind, name, **fields):
        return {"apiVersion": "v1", "kind": kind, "metadata": {"name": name, "namespace": namespace}, **fields}

    rendered = []
    created = False
    pod = None
    code = None
    live_lock.acquire(args.context, namespace, namespace, "check")
    try:
        # A name collision must fail. Never adopt an existing test namespace.
        oc("create", "-f", "-", data=json.dumps({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}}).encode())
        created = True
        with tempfile.TemporaryDirectory(prefix="count-private-") as private:
            key, cert = Path(private) / "server.key", Path(private) / "server.crt"
            subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-keyout", str(key), "-out", str(cert), "-days", "2", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            password = secrets.token_urlsafe(32)
            dsn = "postgres://postgres:" + password + "@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt"
            ca = json.loads(oc("get", "configmap", "kube-root-ca.crt", "-o", "json"))["data"]["ca.crt"]
            apply({"apiVersion": "v1", "kind": "List", "items": [
                obj("ResourceQuota", "check", spec={"hard": {"pods": "1", "limits.cpu": "1500m", "limits.memory": "3584Mi", "limits.ephemeral-storage": "8Gi"}}),
                obj("Secret", "cli-test-postgres", stringData={"password": password, "dsn": dsn, "url": dsn}),
                obj("Secret", "database-tls", stringData={"server.key": key.read_text(), "server.crt": cert.read_text()}),
                obj("ConfigMap", "database-ca", data={"server.crt": cert.read_text()}),
                obj("Secret", "count-credentials", stringData={"ca.crt": ca, "count": "", "allocator": "", "workload": ""}), job]})
        deadline = time.monotonic() + 1200
        while pod is None:
            pods = json.loads(oc("get", "pods", "-l", "job-name=check", "-o", "json"))["items"]
            if pods:
                pod = pods[0]["metadata"]["name"]
                break
            if time.monotonic() > deadline:
                raise RuntimeError("No test Pod; inspect the existing Job")
            time.sleep(1)
        oc("wait", "--for=condition=Ready", "pod/" + pod, "--timeout=120s", timeout=135)
        oc("exec", "-i", pod, "--", "tar", "xf", "-", "-C", "/work/application", data=(result / "source.tar").read_bytes(), timeout=120)
        oc("exec", pod, "--", "touch", "/work/start")

        def exists(name):
            try:
                oc("exec", pod, "--", "test", "-f", "/work/" + name)
                return True
            except subprocess.CalledProcessError:
                return False

        def wait_file(name):
            last = 0
            while not exists(name):
                state = json.loads(oc("get", "job", "check", "-o", "json"))
                if any(x["type"] == "Failed" and x["status"] == "True" for x in state.get("status", {}).get("conditions", [])):
                    raise RuntimeError("Existing test Job failed")
                if name != "result" and exists("result"):
                    raise RuntimeError("Test setup failed before " + name)
                if time.monotonic() > deadline:
                    raise RuntimeError("Test deadline reached; inspect the existing Job")
                if time.monotonic() - last > 30:
                    print("Waiting for " + name, flush=True)
                    last = time.monotonic()
                time.sleep(5)

        wait_file("rbac-ready")
        namespaced = {"ServiceAccount", "Role", "RoleBinding"}
        cluster = {"ClusterRole", "ClusterRoleBinding", "ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding"}
        seen = set()
        for worker in ["namespace-allocation", "gateway-workload", "sandbox-count"]:
            data = oc("exec", pod, "--", "cat", "/work/" + worker + ".json")
            (result / (worker + ".json")).write_bytes(data)
            for item in json.loads(data)["items"]:
                kind, meta = item["kind"], item["metadata"]
                if kind not in namespaced | cluster:
                    continue
                if kind in namespaced and meta.get("namespace") != namespace:
                    raise RuntimeError("Generated resource has a different namespace")
                if kind in cluster and not meta["name"].startswith(namespace + "."):
                    raise RuntimeError("Generated cluster resource has a different owner")
                identity = (kind, meta["name"])
                if identity not in seen:
                    seen.add(identity)
                    rendered.append(item)
        document = {"apiVersion": "v1", "kind": "List", "items": rendered}
        (result / "permissions.json").write_text(json.dumps(document, indent=2) + "\n")
        apply(document)
        credentials = {"ca.crt": ca}
        for key, worker in {"allocator": "namespace-allocation", "workload": "gateway-workload", "count": "sandbox-count"}.items():
            credentials[key] = oc("create", "token", "hypershell-" + worker, "--duration=25m").decode().strip()
        apply(obj("Secret", "count-credentials", stringData=credentials))
        credentials.clear()
        oc("exec", pod, "--", "touch", "/work/live-ready")
        wait_file("result")
        code = int(oc("exec", pod, "--", "cat", "/work/result").decode().strip())
        for name in ["test.log", "generated.tar", "first.sha256", "second.sha256", "after.sha256"]:
            if exists(name):
                (result / name).write_bytes(oc("exec", pod, "--", "cat", "/work/" + name, timeout=90))
        if code == 0 and "--- PASS: TestNamespaceCountWithLiveKubernetes (" not in (result / "test.log").read_text():
            raise RuntimeError("The required live test did not pass; a skipped or missing test is not a pass")
        oc("exec", pod, "--", "touch", "/work/collected")
        oc("wait", "--for=condition=" + ("Complete" if code == 0 else "Failed"), "job/check", "--timeout=60s", timeout=75)
        (result / "job-final.json").write_bytes(oc("get", "job", "check", "-o", "json"))
        print((result / "test.log").read_text()[-8000:], flush=True)
    finally:
        if pod and not (result / "test.log").exists():
            try:
                (result / "test.log").write_bytes(oc("exec", pod, "--", "cat", "/work/test.log"))
            except (subprocess.SubprocessError, OSError):
                pass
        # Keep admission and allocator roles until all owned namespaces are gone.
        if created:
            # Stop the producer before fallback cleanup. Never race allocation.
            oc("delete", "job", "check", "--ignore-not-found", "--cascade=foreground", "--wait=true", "--timeout=90s", timeout=105)
            owned = json.loads(oc("get", "namespaces", "-l", "stego.dev/allocator=" + marker, "-o", "json"))["items"]
            bindings = json.loads(oc("get", "clusterrolebindings", "-l", "stego.dev/allocator=" + marker, "-o", "json"))["items"]
            fallback = []
            for item in owned:
                meta = item["metadata"]
                labels = meta.get("labels", {})
                if not meta["name"].startswith("openshell-") or labels.get("stego.dev/allocation-profile") != "gateway" or labels.get("app.kubernetes.io/managed-by") != "hypershell-gateway-controller" or not labels.get("hypershell.redhat.io/gateway-id"):
                    raise RuntimeError("Unexpected namespace identity; keep resources for inspection")
            for item in bindings:
                meta = item["metadata"]
                if not meta["name"].startswith(namespace + ".hypershell-namespace-allocation.openshell-"):
                    raise RuntimeError("Unexpected binding identity; keep resources for inspection")
                oc("delete", "clusterrolebinding", meta["name"], "--wait=true", "--timeout=30s")
                fallback.append({"kind": "ClusterRoleBinding", "name": meta["name"], "uid": meta["uid"]})
            for item in owned:
                meta = item["metadata"]
                oc("delete", "namespace", meta["name"], "--wait=false")
                oc("wait", "--for=delete", "namespace/" + meta["name"], "--timeout=90s", timeout=105)
                fallback.append({"kind": "Namespace", "name": meta["name"], "uid": meta["uid"]})
            for resource in ["namespaces", "clusterrolebindings"]:
                if json.loads(oc("get", resource, "-l", "stego.dev/allocator=" + marker, "-o", "json"))["items"]:
                    raise RuntimeError("Owned resources remain after cleanup")
            for item in reversed(rendered):
                oc("delete", item["kind"], item["metadata"]["name"], "--ignore-not-found", "--wait=true", "--timeout=30s")
            oc("delete", "namespace", namespace, "--wait=false")
            oc("wait", "--for=delete", "namespace/" + namespace, "--timeout=90s", timeout=105)
            (result / "cleanup.json").write_text(json.dumps({"namespace_absent": namespace, "allocator_marker": marker, "gateway_namespaces_absent": True, "gateway_cluster_bindings_absent": True, "fallback": fallback}) + "\n")
        live_lock.release(args.context, namespace)
    if code != 0:
        raise SystemExit("Live namespace count check failed; see " + str(result))
    print("Live namespace count check passed. Results: " + str(result), flush=True)


if __name__ == "__main__":
    main()
