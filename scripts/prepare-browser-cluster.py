#!/usr/bin/env python3
"""Install generated cluster resources with the operator's explicit context.

The test Pod never supplies a manifest to this process. Render from the frozen
application source, then retain the exact cluster manifests for the Pod to check.
This script does not make the rest of the browser fixture a restricted CI runner.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
from urllib.parse import quote


def cluster_items(manifest, namespace):
    if len(manifest) > 2 << 20:
        raise RuntimeError("The cluster manifest exceeds the fixture limit")
    document = json.loads(manifest)
    if not isinstance(document, dict) or document.get("apiVersion") != "v1" or document.get("kind") != "List" or not isinstance(document.get("items"), list) or len(document["items"]) > 64:
        raise RuntimeError("The renderer did not return a bounded resource list")
    versions = {"ClusterRole": "rbac.authorization.k8s.io/v1", "ClusterRoleBinding": "rbac.authorization.k8s.io/v1",
                "ValidatingAdmissionPolicy": "admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicyBinding": "admissionregistration.k8s.io/v1"}
    for item in document["items"]:
        if not isinstance(item, dict):
            raise RuntimeError("A cluster resource is invalid")
        metadata = item.get("metadata", {})
        if item.get("kind") not in versions or item.get("apiVersion") != versions[item["kind"]] or not isinstance(metadata, dict) or "namespace" in metadata or not isinstance(metadata.get("name"), str) or not metadata["name"].startswith(namespace + "."):
            raise RuntimeError("A cluster resource is outside the fixture installation")
    return document["items"]


def require_absent(resources, oc):
    seen = set()
    for item in resources:
        key = (item["kind"], item["metadata"]["name"])
        if key in seen or oc("get", *key, "--ignore-not-found", "-o", "json").strip():
            raise RuntimeError("A cluster installation resource already exists or is repeated")
        seen.add(key)


def remove_resources(record, namespace, oc):
    paths = {"ClusterRole": "/apis/rbac.authorization.k8s.io/v1/clusterroles/",
             "ClusterRoleBinding": "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/",
             "ValidatingAdmissionPolicy": "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicies/",
             "ValidatingAdmissionPolicyBinding": "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicybindings/"}
    if record.get("namespace") != namespace or not isinstance(record.get("resources"), list):
        raise RuntimeError("The cluster installation journal has a different owner")
    # Validate the complete journal before any deletion.
    for item in record["resources"]:
        if item.get("kind") not in paths or not isinstance(item.get("name"), str) or not item["name"].startswith(namespace + ".") or not isinstance(item.get("uid"), str) or not item["uid"]:
            raise RuntimeError("The cluster installation journal has an invalid identity")
    for item in reversed(record["resources"]):
        raw = oc("get", item["kind"], item["name"], "--ignore-not-found", "-o", "json")
        if not raw.strip():
            continue
        if json.loads(raw)["metadata"]["uid"] != item["uid"]:
            raise RuntimeError("A cluster installation resource was replaced; keep it for inspection")
        options = {"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": {"uid": item["uid"]}, "propagationPolicy": "Background"}
        oc("delete", "--raw=" + paths[item["kind"]] + quote(item["name"], safe=""), "-f", "-", data=json.dumps(options).encode())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--fs-group", type=int)
    parser.add_argument("--results", required=True, type=Path)
    parser.add_argument("--workload", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    parser.add_argument("--render-only", action="store_true", help="Render and record manifests without cluster requests")
    args = parser.parse_args()
    if args.cleanup and args.render_only:
        parser.error("Cleanup and render-only cannot be combined")
    if not re.fullmatch(r"stego-service-[a-z0-9-]{1,40}", args.namespace) or (not args.cleanup and (args.fs_group is None or not 0 < args.fs_group < 2**31)):
        raise RuntimeError("Require the dedicated fixture namespace and file group")

    def oc(*words, data=None):
        return subprocess.run(["oc", "--context=" + args.context, "--request-timeout=20s", *words],
                              input=data, capture_output=True, check=True, timeout=30).stdout

    journal = args.results / "cluster-installation.json"
    if args.cleanup:
        if journal.exists():
            remove_resources(json.loads(journal.read_text()), args.namespace, oc)
        return
    source = Path(__file__).resolve().parent.parent
    destination = args.results / "cluster-manifests"
    destination.mkdir(mode=0o700)
    environment = dict(os.environ, GOMAXPROCS="1", GOMEMLIMIT="256MiB", GOWORK="off")
    binaries = {}
    record = {"namespace": args.namespace, "source_sha256": {}, "manifests": {}, "resources": []}

    def save():
        temporary = journal.with_suffix(".tmp")
        temporary.write_text(json.dumps(record, indent=2) + "\n")
        os.replace(temporary, journal)

    try:
        for name, directory in [("api", source), ("console", source / "console")]:
            binary = args.results / (name + "-render")
            binaries[name] = binary
            # The generated renderer uses only the standard library. Application
            # compilation and all workload tests stay in the bounded cluster Job.
            subprocess.run(["go", "build", "-p=1", "-mod=readonly", "-trimpath", "-o", str(binary), "./out/deploy/render"],
                           cwd=directory, env=environment, check=True, timeout=45)
            for path in sorted((directory / "out/deploy/render").iterdir()):
                if path.is_file():
                    record["source_sha256"][str(path.relative_to(source))] = hashlib.sha256(path.read_bytes()).hexdigest()

        targets = [("hypershell", "api", []), ("hypershell-console", "console", []),
                   ("hypershell-provisioner", "api", ["--rpc-process", "provisioner"])]
        if args.workload:
            targets += [("hypershell-namespace-allocation", "api", ["--worker", "namespace-allocation", "--egress", "kubernetes=192.0.2.1:443"]),
                        ("hypershell-gateway-identity", "api", ["--worker", "gateway-identity"]),
                        ("hypershell-gateway-workload", "api", ["--worker", "gateway-workload", "--egress", "kubernetes=192.0.2.1:443", "--egress", "gateway-postgres=192.0.2.2:5432"])]
        resources = []
        for name, module, target in targets:
            # Cluster roles and policies have no image or network endpoint. The
            # Pod checks this output against a render with its actual deployment
            # arguments before it can apply the corresponding namespace scope.
            command = [str(binaries[module]), "--namespace", args.namespace, "--fs-group", str(args.fs_group),
                       "--image", "registry.example.test/fixture@sha256:" + "a" * 64, "--scope", "cluster", *target]
            manifest = subprocess.check_output(command, env=environment, timeout=5)
            resources.extend(cluster_items(manifest, args.namespace))
            (destination / (name + ".json")).write_bytes(manifest)
            record["manifests"][name] = hashlib.sha256(manifest).hexdigest()
        # Check all names before the first write. Never adopt or replace a
        # resource that already exists, even if it has the expected name.
        if args.render_only:
            record["mode"] = "render-only"
            save()
            return
        require_absent(resources, oc)
        save()
        for item in resources:
            created = json.loads(oc("create", "-f", "-", "-o", "json", data=json.dumps(item).encode()))
            record["resources"].append({"kind": created["kind"], "name": created["metadata"]["name"], "uid": created["metadata"]["uid"]})
            save()
        for item in resources:
            if item["kind"] != "ValidatingAdmissionPolicy":
                continue
            for _ in range(40):
                policy = json.loads(oc("get", item["kind"], item["metadata"]["name"], "-o", "json"))
                status = policy.get("status", {})
                if status.get("observedGeneration") == policy["metadata"]["generation"] and "typeChecking" in status:
                    if status["typeChecking"].get("expressionWarnings"):
                        raise RuntimeError("A generated admission policy has type-check warnings")
                    break
                time.sleep(0.5)
            else:
                raise RuntimeError("Generated admission policy type checking did not finish")
        record["policy_type_checks"] = "success"
        save()
        print("Operator installed " + str(len(resources)) + " generated cluster resources.")
    finally:
        for binary in binaries.values():
            binary.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
