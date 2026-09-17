#!/usr/bin/env python3
"""Create the restricted CI identity and write an expiring private kubeconfig.

Run as the operator. This script never prints a token. It does not grant CI
permission to install cluster roles, admission policies, or database operators.
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--github-repository", help="Store JSHELL_CI_KUBECONFIG in this repository")
    parser.add_argument("--github-environment", help="Store the secret in this GitHub environment")
    args = parser.parse_args()
    if args.github_environment and not args.github_repository:
        parser.error("--github-environment requires --github-repository")
    root = Path(__file__).resolve().parent.parent
    document = json.loads((root / "deploy/ci/jshell.json").read_text())

    def oc(*words, data=None):
        return subprocess.run(["oc", "--context=" + args.context, "--request-timeout=20s", *words],
                              input=data, capture_output=True, check=True, timeout=30).stdout

    for item in document["items"]:
        meta = item["metadata"]
        target = [item["kind"], meta["name"]]
        if "namespace" in meta:
            target += ["-n", meta["namespace"]]
        existing = oc("get", *target, "--ignore-not-found", "-o", "json")
        if existing:
            current = json.loads(existing)
            if current["metadata"].get("labels", {}).get("app.kubernetes.io/managed-by") != "stego-ci":
                raise RuntimeError("CI setup refuses an existing resource with a different owner")
            # Never reset a Lease that can belong to a running test.
            if item["kind"] == "Lease":
                continue
            # The operator owns the declared fields of these named CI objects.
            oc("apply", "--server-side", "--force-conflicts", "--field-manager=stego-ci", "-f", "-", data=json.dumps(item).encode())
        else:
            oc("create", "--field-manager=stego-ci", "-f", "-", data=json.dumps(item).encode())
    # Allow the API server to finish policy type checking before issuing a token.
    for policy_name in ["stego-ci-bounded-jobs", "stego-ci-no-legacy-tokens"]:
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
    token = oc("-n", "stego-ci-access", "create", "token", "hypershell-ci", "--duration=1h").decode().strip()
    claim = token.split(".")[1]
    payload = json.loads(base64.urlsafe_b64decode(claim + "=" * (-len(claim) % 4)))
    now = datetime.now(timezone.utc).timestamp()
    if payload.get("sub") != "system:serviceaccount:stego-ci-access:hypershell-ci" or not now < payload.get("exp", 0) <= now + 3660:
        raise RuntimeError("The CI token has an unexpected subject or expiry")
    selected = json.loads(oc("config", "view", "--raw", "--minify", "-o", "jsonpath={.clusters[0].cluster}"))
    server = selected["server"]
    if not server.startswith("https://") or selected.get("insecure-skip-tls-verify", False):
        raise RuntimeError("The selected cluster must have a verified HTTPS connection")
    cluster = {"server": server}
    if selected.get("certificate-authority-data"):
        cluster["certificate-authority-data"] = selected["certificate-authority-data"]
    elif selected.get("certificate-authority"):
        cluster["certificate-authority-data"] = base64.b64encode(Path(selected["certificate-authority"]).read_bytes()).decode()
    # Without a private CA, oc verifies the certificate with the system roots.
    if selected.get("tls-server-name"):
        cluster["tls-server-name"] = selected["tls-server-name"]
    config = {"apiVersion": "v1", "kind": "Config", "current-context": "jshell-ci",
              "contexts": [{"name": "jshell-ci", "context": {"cluster": "jshell", "user": "hypershell-ci", "namespace": "stego-ci"}}],
              "clusters": [{"name": "jshell", "cluster": cluster}],
              "users": [{"name": "hypershell-ci", "user": {"token": token}}]}
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # Replace the file atomically. Never follow an existing file symlink.
    with tempfile.NamedTemporaryFile(mode="w", prefix=".kubeconfig-", dir=args.output.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            os.fchmod(output.fileno(), 0o600)
            json.dump(config, output)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
            os.replace(temporary, args.output)
        finally:
            temporary.unlink(missing_ok=True)
    if args.github_repository:
        command = ["gh", "secret", "set", "JSHELL_CI_KUBECONFIG", "--repo", args.github_repository]
        if args.github_environment:
            command += ["--env", args.github_environment]
        subprocess.run(command,
                       input=json.dumps(config).encode(), capture_output=True, check=True, timeout=30)
        print("Updated GitHub secret JSHELL_CI_KUBECONFIG for " + args.github_repository +
              (" environment " + args.github_environment if args.github_environment else ""))
    expiry = datetime.fromtimestamp(payload["exp"], timezone.utc).isoformat()
    print("CI identity: system:serviceaccount:stego-ci-access:hypershell-ci")
    print("CI token expires: " + expiry)
    print("Private kubeconfig: " + str(args.output))


if __name__ == "__main__":
    main()
