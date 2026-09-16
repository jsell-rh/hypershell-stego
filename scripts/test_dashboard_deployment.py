#!/usr/bin/env python3
"""Check that the dashboard verifier rejects broken generated boundaries."""
import copy
import json
import pathlib
import runpy
import tempfile
import unittest

verify = runpy.run_path(str(pathlib.Path(__file__).with_name("check-dashboard-deployment.py")))["verify"]
IMAGE = "ghcr.io/jsell-rh/hypershell-stego-dashboard-ci@sha256:" + "a" * 64


def fixture():
    labels = {"app": "browser"}
    pod = {
        "automountServiceAccountToken": False,
        "containers": [
            {"name": "browser", "envFrom": [{"secretRef": {"name": "browser-env"}}], "volumeMounts": [{"name": "files"}, {"name": "tmp"}]},
            {"name": "application", "image": IMAGE, "envFrom": [{"secretRef": {"name": "app-env"}}], "env": [{"name": "LISTEN_ADDRESS", "value": "127.0.0.1"}, {"name": "PORT", "value": "8000"}], "volumeMounts": [{"name": "application-files"}, {"name": "application-tmp"}]},
        ],
        "volumes": [{"name": "files", "secret": {"secretName": "browser-files"}}, {"name": "tmp"}, {"name": "application-files", "secret": {"secretName": "app-files"}}, {"name": "application-tmp"}],
    }
    return {"items": [
        {"kind": "Deployment", "spec": {"template": {"metadata": {"labels": labels}, "spec": pod}}},
        {"kind": "Service", "spec": {"selector": labels, "ports": [{"port": 8443}]}},
        {"kind": "NetworkPolicy", "spec": {"podSelector": {"matchLabels": labels}, "policyTypes": ["Ingress", "Egress"], "ingress": [{"ports": [{"port": 8443}]}]}},
    ]}


class Boundary(unittest.TestCase):
    def check(self, document):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "deployment.json").write_text(json.dumps(document))
            (root / "application-image").write_text(IMAGE)
            verify(root)
            self.assertTrue((root / "deployment-verification.json").exists())

    def test_valid(self):
        self.check(fixture())

    def test_broken_boundaries(self):
        document = fixture()
        mutations = []
        missing = copy.deepcopy(document); missing["items"].pop(); mutations.append(missing)
        selector = copy.deepcopy(document); selector["items"][2]["spec"]["podSelector"] = {"matchLabels": {"app": "other"}}; mutations.append(selector)
        ingress = copy.deepcopy(document); ingress["items"][2]["spec"]["ingress"][0]["ports"][0]["port"] = 8000; mutations.append(ingress)
        shared = copy.deepcopy(document); shared["items"][0]["spec"]["template"]["spec"]["volumes"][2]["secret"]["secretName"] = "browser-files"; mutations.append(shared)
        identity = copy.deepcopy(document); identity["items"][0]["spec"]["template"]["spec"]["automountServiceAccountToken"] = True; mutations.append(identity)
        image = copy.deepcopy(document); image["items"][0]["spec"]["template"]["spec"]["containers"][1]["image"] = "wrong"; mutations.append(image)
        for index, mutation in enumerate(mutations):
            with self.subTest(index=index), self.assertRaises(AssertionError):
                self.check(mutation)


if __name__ == "__main__":
    unittest.main()
