#!/usr/bin/env python3
"""Check CNPG database reconciliation in an isolated, bounded OpenShift Job.

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
    result = Path(tempfile.mkdtemp(prefix="hypershell-cnpg-live-"))
    namespace = "stego-cnpg-live-" + uuid.uuid4().hex[:8]
    marker = hashlib.sha256((namespace + ".hypershell-namespace-allocation").encode()).hexdigest()[:32]
    (result / "namespace").write_text(namespace + "\n")
    print(f"Results: {result}\nNamespace: {namespace}", flush=True)

    def oc(*words, data=None, timeout=45):
        command = ["oc", "--context=" + args.context, "--request-timeout=30s", "-n", namespace, *words]
        return subprocess.run(command, input=data, capture_output=True, check=True, timeout=timeout).stdout

    def apply(document, target_namespace=None):
        # Do not include a Secret body in an exception or log.
        try:
            return oc("apply", "-f", "-", "-n", target_namespace or namespace, data=json.dumps(document).encode())
        except subprocess.CalledProcessError:
            raise RuntimeError("Kubernetes apply failed; inspect the named test resources") from None

    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    required = {"acceptance/cnpg_database_test.go", "acceptance/allocated_kubernetes_fixture_test.go", "scripts/check-cnpg-database.py"}
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
for worker in namespace-allocation database; do
  /work/render --namespace "$STEGO_TEST_NAMESPACE" --image "$STEGO_TEST_IDLE_IMAGE" --worker "$worker" --egress "kubernetes=$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT" > "/work/$worker.json"
done
touch /work/rbac-ready
while [ ! -f /work/live-ready ]; do sleep 1; done
while [ ! -s /cnpg-credentials/namespace-allocation ] || [ ! -s /cnpg-credentials/database ] || [ ! -s /cnpg-credentials/driver ]; do sleep 1; done
export PATH=/work/bin:$PATH
go test -v -race -count=1 -timeout=10m ./acceptance -run '^(TestCNPGDatabaseWorkloadAndOfflineDeletion|TestLocalDatabaseSelectionAndRollback|TestLocalDatabaseRegistrationThroughRESTAndGRPC)$'
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
        "GOWORK": "off", "STEGO_REQUIRE_CNPG": "1", "STEGO_REQUIRE_KUBERNETES": "1",
        "STEGO_TEST_ALLOCATED_FIXTURE": "1", "STEGO_TEST_KUBECONFIG": "/cnpg-credentials/kubeconfig",
        "STEGO_TEST_KUBERNETES_CONTEXT": args.context,
        "STEGO_TEST_KUBERNETES_URL": "https://kubernetes.default.svc",
        "STEGO_TEST_IDLE_IMAGE": test["image"],
    }.items()]
    test["resources"]["limits"]["ephemeral-storage"] = "6Gi"
    test["volumeMounts"].append({"name": "cnpg-credentials", "mountPath": "/cnpg-credentials", "readOnly": True})
    volumes.append({"name": "cnpg-credentials", "secret": {"secretName": "cnpg-credentials", "defaultMode": 416}})
    job = {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": "check", "namespace": namespace}, "spec": {
        "backoffLimit": 0, "activeDeadlineSeconds": 1200, "template": {"spec": {
            "restartPolicy": "Never", "automountServiceAccountToken": False,
            "securityContext": {"runAsNonRoot": True, "seccompProfile": {"type": "RuntimeDefault"}},
            "initContainers": [pg], "containers": [test], "volumes": volumes}}}}
    (result / "job.json").write_text(json.dumps(job, indent=2) + "\n")
    def obj(kind, name, **fields):
        return {"apiVersion": "v1", "kind": kind, "metadata": {"name": name, "namespace": namespace}, **fields}

    rendered = []
    operator = result / "cnpg-test-operator.py"
    operator.write_bytes((root / "scripts/cnpg-test-operator.py").read_bytes())
    def cnpg(action, *extra):
        subprocess.run(["python3", str(operator), action, "--context", args.context, "--namespace", namespace, "--evidence", str(result), *extra], check=True, timeout=180)
    created = False
    pod = None
    code = None
    live_lock.acquire(args.context, namespace, namespace, "check")
    try:
        # A name collision must fail. Never adopt an existing test namespace.
        oc("create", "-f", "-", data=json.dumps({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}}).encode())
        created = True
        with tempfile.TemporaryDirectory(prefix="cnpg-private-") as private:
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
                obj("Secret", "cnpg-credentials", stringData={"ca.crt": ca, "namespace-allocation": "", "database": "", "driver": "", "kubeconfig": ""}), job]})
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
        oc("exec", pod, "--", "mkdir", "-p", "/work/bin")
        import shutil
        with Path(shutil.which("oc")).resolve().open("rb") as binary:
            subprocess.run(["oc", "--context=" + args.context, "-n", namespace, "exec", "-i", pod, "--", "sh", "-c", "cat > /work/bin/kubectl && chmod 700 /work/bin/kubectl"], stdin=binary, stdout=subprocess.DEVNULL, check=True, timeout=120)
        oc("exec", pod, "--", "touch", "/work/start")

        def exists(name):
            try:
                oc("exec", pod, "--", "test", "-f", "/work/" + name)
                return True
            except subprocess.CalledProcessError:
                return False

        access_sequence = 0
        def handle_access_request():
            nonlocal access_sequence
            if not exists("allocator-access-request.json"):
                return
            request = json.loads(oc("exec", pod, "--", "cat", "/work/allocator-access-request.json"))
            if request.get("sequence") == access_sequence:
                return
            if request.get("sequence") != access_sequence + 1 or type(request.get("allow_delete")) is not bool:
                raise RuntimeError("Invalid allocator access request")
            role_name = namespace + ".hypershell-namespace-allocation"
            role = json.loads(oc("get", "clusterrole", role_name, "-o", "json"))
            if role["metadata"]["uid"] != allocator_role_uid:
                raise RuntimeError("Allocator role identity changed")
            # The test can toggle only namespace deletion. It cannot supply a
            # role name or rules. The host uses the frozen generated role.
            rules = json.loads(json.dumps(allocator_role_rules))
            changed = False
            for rule in rules:
                if rule.get("apiGroups") == [""] and rule.get("resources") == ["namespaces"]:
                    if "delete" not in rule["verbs"]:
                        raise RuntimeError("Generated allocator cannot delete namespaces")
                    if not request["allow_delete"]:
                        rule["verbs"].remove("delete")
                    changed = True
            if not changed:
                raise RuntimeError("Generated allocator namespace rule is missing")
            patch = [{"op": "test", "path": "/metadata/uid", "value": allocator_role_uid},
                     {"op": "test", "path": "/metadata/resourceVersion", "value": role["metadata"]["resourceVersion"]},
                     {"op": "replace", "path": "/rules", "value": rules}]
            oc("patch", "clusterrole", role_name, "--type=json", "-p", json.dumps(patch))
            access_sequence = request["sequence"]
            (result / ("allocator-access-" + str(access_sequence) + ".json")).write_text(json.dumps(request) + "\n")
            oc("exec", "-i", pod, "--", "sh", "-c", "cat > /work/allocator-access-ack.tmp && mv /work/allocator-access-ack.tmp /work/allocator-access-ack.json", data=json.dumps(request).encode())

        def wait_file(name):
            last = 0
            while not exists(name):
                if name == "result":
                    handle_access_request()
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
        for worker in ["namespace-allocation", "database"]:
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
        allocator_role = json.loads(oc("get", "clusterrole", namespace + ".hypershell-namespace-allocation", "-o", "json"))
        allocator_role_uid, allocator_role_rules = allocator_role["metadata"]["uid"], allocator_role["rules"]
        declared = next(item for item in rendered if item["kind"] == "ClusterRole" and item["metadata"]["name"] == namespace + ".hypershell-namespace-allocation")
        if allocator_role_rules != declared["rules"]:
            raise RuntimeError("Allocator role differs from generated rules")
        driver_role = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": {"name": namespace + ".test-driver"}, "rules": [
            {"apiGroups": ["rbac.authorization.k8s.io"], "resources": ["clusterroles"], "resourceNames": [namespace + ".hypershell-namespace-allocation"], "verbs": ["get"]}]}
        driver_binding = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": {"name": namespace + ".test-driver"}, "subjects": [{"kind": "ServiceAccount", "name": "service-check", "namespace": namespace}], "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": driver_role["metadata"]["name"]}}
        driver_sa = obj("ServiceAccount", "service-check")
        rendered += [driver_sa, driver_role, driver_binding]
        apply({"apiVersion": "v1", "kind": "List", "items": [driver_sa, driver_role, driver_binding]})
        credentials = {"ca.crt": ca}
        for worker in ["namespace-allocation", "database"]:
            credentials[worker] = oc("create", "token", "hypershell-" + worker, "--duration=25m").decode().strip()
        credentials["driver"] = oc("create", "token", "service-check", "--duration=25m").decode().strip()
        credentials["kubeconfig"] = json.dumps({"apiVersion": "v1", "kind": "Config", "current-context": args.context,
            "contexts": [{"name": args.context, "context": {"cluster": "test", "user": "driver", "namespace": namespace}}],
            "clusters": [{"name": "test", "cluster": {"server": "https://kubernetes.default.svc", "certificate-authority": "/cnpg-credentials/ca.crt"}}],
            "users": [{"name": "driver", "user": {"tokenFile": "/cnpg-credentials/driver"}}]})
        apply(obj("Secret", "cnpg-credentials", stringData=credentials))
        credentials.clear()
        oc("exec", pod, "--", "touch", "/work/live-ready")
        wait_file("cnpg-request.json")
        request = json.loads(oc("exec", pod, "--", "cat", "/work/cnpg-request.json"))
        cnpg("prepare", "--database-id", request["id"])
        plan = json.loads((result / "cnpg-plan.json").read_text())
        if request["namespace"] != plan["database_namespace"]:
            raise RuntimeError("CNPG database namespace differs from its ID")
        cnpg("install")
        driver_role["rules"].append({"apiGroups": [""], "resources": ["namespaces"], "resourceNames": [request["namespace"]], "verbs": ["get", "list", "watch"]})
        apply(driver_role)
        oc("exec", pod, "--", "touch", "/work/operator-ready")
        allocated = None
        allocation_deadline = time.monotonic() + 60
        while allocated is None:
            data = oc("get", "namespace", request["namespace"], "--ignore-not-found", "-o", "json")
            if data:
                allocated = json.loads(data)
                break
            if time.monotonic() > allocation_deadline:
                raise RuntimeError("Database namespace was not allocated")
            time.sleep(1)
        labels = allocated["metadata"].get("labels", {})
        if labels.get("stego.dev/allocator") != marker or labels.get("hypershell.redhat.io/database-id") != request["id"] or labels.get("stego.dev/allocation-profile") != "database":
            raise RuntimeError("Database namespace has a different owner")
        local_role = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": {"name": "test-driver", "namespace": request["namespace"]}, "rules": [
            {"apiGroups": [""], "resources": ["pods"], "verbs": ["get", "list", "watch", "create", "delete"]},
            {"apiGroups": [""], "resources": ["pods/log"], "verbs": ["get"]},
            {"apiGroups": [""], "resources": ["secrets"], "resourceNames": ["openshell-db-app"], "verbs": ["get"]},
            {"apiGroups": ["postgresql.cnpg.io"], "resources": ["clusters"], "verbs": ["get", "list", "watch", "patch"]}]}
        local_binding = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": {"name": "test-driver", "namespace": request["namespace"]}, "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "test-driver"}, "subjects": [{"kind": "ServiceAccount", "name": "service-check", "namespace": namespace}]}
        (result / "driver-permissions.json").write_text(json.dumps([driver_sa, driver_role, driver_binding, local_role, local_binding], indent=2) + "\n")
        apply({"apiVersion": "v1", "kind": "List", "items": [local_role, local_binding]}, request["namespace"])
        oc("exec", pod, "--", "touch", "/work/driver-ready")
        wait_file("result")
        code = int(oc("exec", pod, "--", "cat", "/work/result").decode().strip())
        for name in ["test.log", "generated.tar", "first.sha256", "second.sha256", "after.sha256"]:
            if exists(name):
                (result / name).write_bytes(oc("exec", pod, "--", "cat", "/work/" + name, timeout=90))
        for required in ["TestCNPGDatabaseWorkloadAndOfflineDeletion", "TestLocalDatabaseSelectionAndRollback", "TestLocalDatabaseRegistrationThroughRESTAndGRPC"]:
            if code == 0 and "--- PASS: " + required + " (" not in (result / "test.log").read_text():
                raise RuntimeError("The required test did not pass: " + required)
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
                profile = labels.get("stego.dev/allocation-profile")
                if profile not in ["gateway", "database"] or not meta["name"].startswith("openshell-" if profile == "gateway" else "openshell-db-") or labels.get("app.kubernetes.io/managed-by") != "hypershell-" + profile + "-controller" or not labels.get("hypershell.redhat.io/" + profile + "-id"):
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
            if (result / "cnpg-plan.json").exists():
                cnpg("remove")
                for item in json.loads((result / "cnpg-created.json").read_text()):
                    words = ["wait", "--for=delete", item["kind"] + "/" + item["metadata"]["name"], "--timeout=90s"]
                    if "namespace" in item["metadata"]:
                        words += ["-n", item["metadata"]["namespace"]]
                    oc(*words, timeout=105)
            for item in reversed(rendered):
                oc("delete", item["kind"], item["metadata"]["name"], "--ignore-not-found", "--wait=true", "--timeout=30s")
            oc("delete", "namespace", namespace, "--wait=false")
            oc("wait", "--for=delete", "namespace/" + namespace, "--timeout=90s", timeout=105)
            (result / "cleanup.json").write_text(json.dumps({"namespace_absent": namespace, "allocator_marker": marker, "allocated_namespaces_absent": True, "allocated_cluster_bindings_absent": True, "fallback": fallback}) + "\n")
        live_lock.release(args.context, namespace)
    if code != 0:
        raise SystemExit("Live CNPG database check failed; see " + str(result))
    print("Live CNPG database check passed. Results: " + str(result), flush=True)


if __name__ == "__main__":
    main()
