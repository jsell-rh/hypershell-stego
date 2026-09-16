#!/usr/bin/env python3
"""Check the private dashboard boundary in the actual generated deployment."""
import json
import pathlib
import sys


def verify(root):
    document = json.loads((root / "deployment.json").read_text())
    items = document["items"]
    deployment = next(item for item in items if item["kind"] == "Deployment")
    services = [item for item in items if item["kind"] == "Service"]
    policies = [item for item in items if item["kind"] == "NetworkPolicy"]
    assert len(services) == len(policies) == 1
    labels = deployment["spec"]["template"]["metadata"]["labels"]
    assert services[0]["spec"]["selector"] == labels
    assert policies[0]["spec"]["podSelector"]["matchLabels"] == labels
    assert set(policies[0]["spec"]["policyTypes"]) == {"Ingress", "Egress"}
    pod = deployment["spec"]["template"]["spec"]
    assert pod["automountServiceAccountToken"] is False
    assert not pod.get("hostNetwork") and not pod.get("shareProcessNamespace")
    assert len(pod["containers"]) == 2
    browser, application = pod["containers"]
    assert application["image"] == (root / "application-image").read_text().strip()
    assert application["name"] == "application" and not application.get("ports")
    env = {item["name"]: item["value"] for item in application["env"]}
    assert env["LISTEN_ADDRESS"] == "127.0.0.1" and env["PORT"] == "8000"
    browser_mounts = {item["name"] for item in browser["volumeMounts"]}
    application_mounts = {item["name"] for item in application["volumeMounts"]}
    assert not browser_mounts.intersection(application_mounts)
    assert application_mounts == {"application-files", "application-tmp"}
    volumes = {item["name"]: item for item in pod["volumes"]}
    browser_secrets = {entry["secretRef"]["name"] for entry in browser["envFrom"]}
    browser_secrets.update(volumes[name]["secret"]["secretName"] for name in browser_mounts if "secret" in volumes[name])
    application_secrets = {entry["secretRef"]["name"] for entry in application["envFrom"]}
    application_secrets.update(volumes[name]["secret"]["secretName"] for name in application_mounts if "secret" in volumes[name])
    assert not browser_secrets.intersection(application_secrets)
    for item in items:
        if item["kind"] == "Service":
            assert len(item["spec"]["ports"]) == 1
            assert item["spec"]["ports"][0]["port"] == 8443
        if item["kind"] == "NetworkPolicy":
            for rule in item["spec"].get("ingress", []):
                assert all(port["port"] == 8443 for port in rule["ports"])
    result = {
        "application_image": application["image"],
        "private_listener": True,
        "separate_mounts": True,
        "browser_service_only": True,
        "scope": "Generated image binding and deployment checks only. No live Pod or rendered dashboard result is claimed.",
    }
    (root / "deployment-verification.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    verify(pathlib.Path(sys.argv[1]))
