"""Check the operator's installation boundary without a cluster or Go build."""

import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("installation", Path(__file__).with_name("prepare-browser-cluster.py"))
installation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installation)


class InstallationBoundary(unittest.TestCase):
    namespace = "stego-service-fixture"
    role = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole",
            "metadata": {"name": namespace + ".worker"}, "rules": []}

    def manifest(self, items):
        return json.dumps({"apiVersion": "v1", "kind": "List", "items": items}).encode()

    def test_preserves_objects_and_empty_scope(self):
        self.assertEqual(installation.cluster_items(self.manifest([self.role]), self.namespace), [self.role])
        self.assertEqual(installation.cluster_items(self.manifest([]), self.namespace), [])

    def test_rejects_wrong_scope_kind_version_or_name(self):
        changes = [{"metadata": {"name": "other.worker"}}, {"metadata": {"name": self.namespace + ".worker", "namespace": ""}},
                   {"metadata": []}, {"kind": "Role"}, {"kind": "Unknown"}, {"apiVersion": "rbac.authorization.k8s.io/v2"}]
        for change in changes:
            item = copy.deepcopy(self.role)
            item.update(change)
            with self.subTest(change=change), self.assertRaises(RuntimeError):
                installation.cluster_items(self.manifest([item]), self.namespace)

    def test_rejects_unbounded_or_invalid_lists(self):
        for content in [b"[]", b'{}', self.manifest([None]), self.manifest([self.role] * 65), b" " * ((2 << 20) + 1)]:
            with self.subTest(length=len(content)), self.assertRaises(RuntimeError):
                installation.cluster_items(content, self.namespace)

    def test_existing_or_repeated_resources_are_not_adopted(self):
        for items, answer in [([self.role], b'{"metadata":{"uid":"foreign"}}'), ([self.role, self.role], b"")]:
            calls = []

            def oc(*words):
                calls.append(words)
                return answer

            with self.assertRaises(RuntimeError):
                installation.require_absent(items, oc)
            self.assertTrue(calls)
            self.assertTrue(all(call[0] == "get" for call in calls))

    def test_read_failure_stops_installation(self):
        def oc(*words):
            raise TimeoutError("test observation failed")

        with self.assertRaises(TimeoutError):
            installation.require_absent([self.role], oc)

    def test_cleanup_uses_the_recorded_uid(self):
        record = {"namespace": self.namespace, "resources": [{"kind": "ClusterRole", "name": self.role["metadata"]["name"], "uid": "original"}]}
        calls = []

        def oc(*words, data=None):
            calls.append((words, data))
            return b'{"metadata":{"uid":"original"}}' if words[0] == "get" else b"{}"

        installation.remove_resources(record, self.namespace, oc)
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[1][0], ("delete", "--raw=/apis/rbac.authorization.k8s.io/v1/clusterroles/" + self.role["metadata"]["name"], "-f", "-"))
        self.assertEqual(json.loads(calls[1][1])["preconditions"], {"uid": "original"})

    def test_cleanup_preserves_replaced_or_foreign_resources(self):
        records = [
            {"namespace": self.namespace, "resources": [{"kind": "ClusterRole", "name": self.namespace + ".worker", "uid": "original"}]},
            {"namespace": self.namespace, "resources": [{"kind": "ClusterRole", "name": "other.worker", "uid": "original"}]},
            {"namespace": "other", "resources": []},
        ]
        for record in records:
            calls = []

            def oc(*words, data=None):
                calls.append(words)
                return b'{"metadata":{"uid":"replacement"}}'

            with self.assertRaises(RuntimeError):
                installation.remove_resources(record, self.namespace, oc)
            self.assertFalse(any(words[0] == "delete" for words in calls))


if __name__ == "__main__":
    unittest.main()
