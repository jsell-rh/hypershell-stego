#!/usr/bin/env python3
"""Create the restricted CI identity and write an expiring private kubeconfig.

Run as the operator. This script never prints a token. The standing CI identity
cannot install cluster roles, admission policies, or database operators. The
optional capacity identity can, but only for objects named for a test run.
"""

import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

MANAGED_BY = "stego-ci"
STANDING_SA = ("stego-ci-access", "hypershell-ci")
CAPACITY_SA = ("stego-ci-access", "hypershell-capacity")
POLICY_NAMES = [
    "stego-ci-bounded-jobs",
    "stego-ci-no-legacy-tokens",
    "stego-ci-capacity-boundary-cluster-objects",
    "stego-ci-capacity-boundary-namespaces",
    "stego-ci-capacity-boundary-namespaced-writes",
]


def mint_token(oc, namespace, name, duration, max_age):
    token = oc("-n", namespace, "create", "token", name, "--duration=" + duration).decode().strip()
    claim = token.split(".")[1]
    payload = json.loads(base64.urlsafe_b64decode(claim + "=" * (-len(claim) % 4)))
    now = datetime.now(timezone.utc).timestamp()
    subject = "system:serviceaccount:" + namespace + ":" + name
    if payload.get("sub") != subject or not now < payload.get("exp", 0) <= now + max_age:
        raise RuntimeError("The " + name + " token has an unexpected subject or expiry")
    return token, payload


def write_kubeconfig(path, server_bundle, context, user, token, namespace):
    config = {"apiVersion": "v1", "kind": "Config", "current-context": context,
              "clusters": [{"name": "jshell", "cluster": server_bundle}],
              "contexts": [{"name": context, "context": {"cluster": "jshell", "user": user,
                                                        "namespace": namespace}}],
              "users": [{"name": user, "user": {"token": token}}]}
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # Replace the file atomically. Never follow an existing file symlink.
    with tempfile.NamedTemporaryFile(mode="w", prefix=".kubeconfig-", dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            os.fchmod(output.fileno(), 0o600)
            json.dump(config, output)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)
    return config


def publish_secret(config, repository, environment, name):
    command = ["gh", "secret", "set", name, "--repo", repository]
    if environment:
        command += ["--env", environment]
    subprocess.run(command, input=json.dumps(config).encode(), capture_output=True,
                   check=True, timeout=30)
    print("Updated GitHub secret " + name + " for " + repository +
          (" environment " + environment if environment else ""))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--capacity-output", type=Path,
                        help="Also mint a 2h capacity identity kubeconfig at this path")
    parser.add_argument("--github-repository", help="Store JSHELL_CI_KUBECONFIG in this repository")
    parser.add_argument("--github-environment", help="Store the secret in this GitHub environment")
    parser.add_argument("--github-capacity-secret", action="store_true",
                        help="Also store JSHELL_CAPACITY_KUBECONFIG")
    args = parser.parse_args()
    if args.github_environment and not args.github_repository:
        parser.error("--github-environment requires --github-repository")
    if args.github_capacity_secret and not (args.github_repository and args.capacity_output):
        parser.error("--github-capacity-secret requires --github-repository and --capacity-output")
    root = Path(__file__).resolve().parent.parent

    def oc(*words, data=None):
        return subprocess.run(["oc", "--context=" + args.context, "--request-timeout=20s", *words],
                              input=data, capture_output=True, check=True, timeout=30).stdout

    for manifest in ["deploy/ci/jshell.json", "deploy/ci/jshell-capacity.json"]:
        document = json.loads((root / manifest).read_text())
        for item in document["items"]:
            meta = item["metadata"]
            target = [item["kind"], meta["name"]]
            if "namespace" in meta:
                target += ["-n", meta["namespace"]]
            existing = oc("get", *target, "--ignore-not-found", "-o", "json")
            if existing:
                current = json.loads(existing)
                if current["metadata"].get("labels", {}).get("app.kubernetes.io/managed-by") != MANAGED_BY:
                    raise RuntimeError("CI setup refuses an existing resource with a different owner")
                # Never reset a Lease that can belong to a running test.
                if item["kind"] == "Lease":
                    continue
                # The operator owns the declared fields of these named CI objects.
                oc("apply", "--server-side", "--force-conflicts", "--field-manager=" + MANAGED_BY, "-f", "-",
                   data=json.dumps(item).encode())
            else:
                oc("create", "--field-manager=" + MANAGED_BY, "-f", "-", data=json.dumps(item).encode())
    # Allow the API server to finish policy type checking before issuing a token.
    for policy_name in POLICY_NAMES:
        for _ in range(20):
            policy = json.loads(oc("get", "validatingadmissionpolicy", policy_name, "-o", "json"))
            status = policy.get("status", {})
            if status.get("observedGeneration") == policy["metadata"]["generation"] and "typeChecking" in status:
                if status["typeChecking"].get("expressionWarnings"):
                    raise RuntimeError("CI admission policy has type-check warnings")
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("CI admission policy type checking did not finish")
    selected = json.loads(oc("config", "view", "--raw", "--minify", "-o", "jsonpath={.clusters[0].cluster}"))
    server = selected["server"]
    if not server.startswith("https://") or selected.get("insecure-skip-tls-verify", False):
        raise RuntimeError("The selected cluster must have a verified HTTPS connection")
    cluster = {"server": server}
    if selected.get("certificate-authority-data"):
        cluster["certificate-authority-data"] = selected["certificate-authority-data"]
    elif selected.get("certificate-authority"):
        cluster["certificate-authority-data"] = base64.b64encode(Path(selected["certificate-authority"]).read_bytes()).decode()
    # Without a private CA, oc verifies the certificate with the system trust store.
    if selected.get("tls-server-name"):
        cluster["tls-server-name"] = selected["tls-server-name"]

    token, payload = mint_token(oc, *STANDING_SA, "1h", 3660)
    config = write_kubeconfig(args.output, cluster, "jshell-ci", "hypershell-ci", token, "stego-ci")
    if args.github_repository:
        publish_secret(config, args.github_repository, args.github_environment,
                       "JSHELL_CI_KUBECONFIG")
    expiry = datetime.fromtimestamp(payload["exp"], timezone.utc).isoformat()
    print("CI identity: system:serviceaccount:" + ":".join(STANDING_SA))
    print("CI token expires: " + expiry)
    print("Private kubeconfig: " + str(args.output))

    if args.capacity_output:
        token, payload = mint_token(oc, *CAPACITY_SA, "2h", 7260)
        capacity = write_kubeconfig(args.capacity_output, cluster, "jshell-capacity",
                                     "hypershell-capacity", token, "stego-ci")
        if args.github_capacity_secret:
            publish_secret(capacity, args.github_repository, args.github_environment,
                           "JSHELL_CAPACITY_KUBECONFIG")
        expiry = datetime.fromtimestamp(payload["exp"], timezone.utc).isoformat()
        print("Capacity identity: system:serviceaccount:" + ":".join(CAPACITY_SA))
        print("Capacity token expires: " + expiry)
        print("Capacity kubeconfig: " + str(args.capacity_output))


if __name__ == "__main__":
    main()
