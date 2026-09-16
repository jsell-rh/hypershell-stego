"""Check the operator's installation boundary without a cluster or Go build."""

import copy
import importlib.util
import json
from pathlib import Path
import unittest
import tempfile
from kubernetes_endpoint_bindings import kubernetes_endpoints

spec = importlib.util.spec_from_file_location("installation", Path(__file__).with_name("prepare-browser-cluster.py"))
installation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installation)


class InstallationBoundary(unittest.TestCase):
    namespace = "stego-service-fixture"
    role = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole",
            "metadata": {"name": namespace + ".worker"}, "rules": []}

    def test_renderer_inventory_includes_imported_library(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            render = root / 'out/deploy/render'
            render.mkdir(parents=True)
            command = render / 'main.go'
            command.write_text('package main')
            self.assertEqual(installation.renderer_sources(root), [command])
            library = root / 'out/deploy/resources.go'
            library.write_text('package deployment')
            (root / 'out/deploy/Containerfile').write_text('FROM scratch')
            self.assertEqual(installation.renderer_sources(root), sorted([command, library]))

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


    def test_policies_are_checked_before_bindings_or_roles(self):
        policy = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicy", "metadata": {"name": self.namespace + ".policy"}}
        binding = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicyBinding", "metadata": {"name": self.namespace + ".policy"}}
        for warnings in [[], [{"warning": "invalid expression"}]]:
            calls = []
            record = {"resources": []}
            def oc(*words, data=None):
                calls.append(words)
                if words[0] == 'create':
                    obj = json.loads(data);obj['metadata']['uid'] = 'created';return json.dumps(obj).encode()
                return json.dumps({'metadata': {'generation': 1}, 'status': {'observedGeneration': 1, 'typeChecking': {'expressionWarnings': warnings}}}).encode()
            if warnings:
                with self.assertRaises(RuntimeError):
                    installation.install_resources([binding, self.role, policy], oc, record, lambda: None)
                self.assertEqual(len(calls), 2)
            else:
                installation.install_resources([binding, self.role, policy], oc, record, lambda: None)
                self.assertEqual([r['kind'] for r in record['resources']], ['ValidatingAdmissionPolicy', 'ValidatingAdmissionPolicyBinding', 'ClusterRole'])
                self.assertEqual([c[0] for c in calls], ['create', 'get', 'create', 'create'])
                self.assertEqual(record['policy_type_checks'], 'success')
            self.assertNotIn('pending_resource', record)


class EndpointSnapshot(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.slices = {"items": [{"endpoints": [{"conditions": {"ready": True}, "addresses": ["192.0.2.10", "2001:db8::10"]}], "ports": [{"name": "https", "protocol": "TCP", "port": 6443}]}]}
        self.service = {"spec": {"clusterIP": "172.30.0.1", "clusterIPs": ["172.30.0.1", "2001:db8:1::1"]}}

    def save(self):
        (self.root / 'kubernetes-endpoints.json').write_text(json.dumps(self.slices))
        (self.root / 'kubernetes-service.json').write_text(json.dumps(self.service))

    def test_job_and_operator_share_exact_addresses(self):
        self.save()
        expected = ["172.30.0.1:443", "192.0.2.10:6443", "[2001:db8:1::1]:443", "[2001:db8::10]:6443"]
        self.assertEqual(kubernetes_endpoints(self.root), expected)
        for name, _, flags in installation.workload_targets(self.root):
            actual = [value.removeprefix('kubernetes=') for value in flags if value.startswith('kubernetes=')]
            self.assertEqual(actual, [] if name == 'hypershell-gateway-identity' else expected)
        self.slices['items'].append(copy.deepcopy(self.slices['items'][0]))
        self.slices['items'].reverse()
        self.save()
        self.assertEqual(kubernetes_endpoints(self.root), expected)

    def test_rejects_unusable_addresses_and_ports(self):
        for address in ['255.255.255.255', '127.0.0.1', '::', 'fe80::1', '224.0.0.1', '::ffff:192.0.2.1', 'fe80::1%eth0', 'database.example']:
            self.slices['items'][0]['endpoints'][0]['addresses'] = [address]
            self.save()
            with self.subTest(address=address), self.assertRaises(ValueError):
                kubernetes_endpoints(self.root)
        self.slices['items'][0]['endpoints'][0]['addresses'] = ['192.0.2.10']
        for port in [0, 65536, True, '443']:
            self.slices['items'][0]['ports'][0]['port'] = port
            self.save()
            with self.subTest(port=port), self.assertRaises(ValueError):
                kubernetes_endpoints(self.root)

    def test_rejects_missing_ready_backend_and_excess(self):
        self.slices['items'][0]['endpoints'][0]['conditions']['ready'] = False
        self.save()
        with self.assertRaises(ValueError):
            installation.workload_targets(self.root)
        self.slices['items'][0]['endpoints'][0]['conditions']['ready'] = True
        self.slices['items'][0]['endpoints'][0]['addresses'] = ['192.0.2.' + str(i) for i in range(1, 17)]
        self.save()
        with self.assertRaises(ValueError):
            kubernetes_endpoints(self.root)
        (self.root / 'kubernetes-endpoints.json').write_bytes(b' ' * ((256 << 10) + 1))
        with self.assertRaises(ValueError):
            kubernetes_endpoints(self.root)


if __name__ == "__main__":
    unittest.main()
