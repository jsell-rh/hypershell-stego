"""Check cleanup observation scope, identity, bounds, and private data removal."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('observation', Path(__file__).with_name('collect-gateway-cleanup.py'))
observation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observation)
MARKER = hashlib.sha256(b'fixture.hypershell-namespace-allocation').hexdigest()[:32]


def namespace(uid='namespace-one'):
    # Namespace list items from the real API carry no kind field.
    return {'metadata': {'name': 'owned', 'uid': uid,
            'deletionTimestamp': '2026-09-21T11:00:00Z', 'labels': {observation.ALLOCATOR_LABEL: MARKER},
            'annotations': {'private': 'PRIVATE_DATA'}}, 'spec': {'finalizers': ['kubernetes']},
            'status': {'conditions': [{'type': 'NamespaceContentRemaining', 'status': 'True',
                                      'reason': 'SomeResourcesRemain', 'message': 'PRIVATE_DATA'}]}}


def pod():
    return {'metadata': {'name': 'pod', 'namespace': 'owned', 'uid': 'pod-one',
            'deletionTimestamp': '2026-09-21T11:00:01Z', 'deletionGracePeriodSeconds': 30,
            'ownerReferences': [{'kind': 'ReplicaSet', 'name': 'rs', 'uid': 'rs-one', 'controller': True}]},
            'spec': {'terminationGracePeriodSeconds': 30, 'env': 'PRIVATE_DATA', 'command': ['PRIVATE_DATA']},
            'status': {'phase': 'Running', 'message': 'PRIVATE_DATA', 'containerStatuses': [
                {'name': 'server', 'ready': False, 'restartCount': 2, 'state': {'terminated': {
                    'reason': 'Completed', 'exitCode': 0, 'signal': 0, 'finishedAt': '2026-09-21T11:00:02Z',
                    'message': 'PRIVATE_DATA', 'containerID': 'PRIVATE_DATA'}},
                 'lastState': {'terminated': {'exitCode': 137, 'signal': 9, 'message': 'PRIVATE_DATA'}}}]}}


def page(items, **extra):
    return SimpleNamespace(stdout=json.dumps({'items': items, **extra}).encode())


class CleanupObservation(unittest.TestCase):
    def collect(self, responses):
        with patch.object(observation.subprocess, 'run', side_effect=responses) as run:
            result = observation.collect('saved-context', 'fixture')
        return result, run

    def test_owned_pod_identity_grace_and_exit_without_private_data(self):
        result, run = self.collect([page([namespace()]), page([pod()]), page([namespace()])])
        self.assertTrue(result['snapshot_complete'])
        self.assertFalse(result['cleanup_proved'])
        self.assertFalse(result['cluster_changes'])
        self.assertNotIn('PRIVATE_DATA', json.dumps(result))
        ns = result['namespaces'][0]
        self.assertEqual(ns['uid'], 'namespace-one')
        self.assertEqual(ns['namespace_finalizers'], ['kubernetes'])
        saved = ns['pods'][0]
        self.assertEqual(saved['uid'], 'pod-one')
        self.assertEqual(saved['owners'][0]['uid'], 'rs-one')
        self.assertEqual(saved['terminationGracePeriodSeconds'], 30)
        self.assertEqual(saved['deletionGracePeriodSeconds'], 30)
        self.assertEqual(saved['containers'][0]['state']['terminated']['exitCode'], 0)
        self.assertEqual(saved['containers'][0]['lastState']['terminated']['signal'], 9)
        calls = run.call_args_list
        self.assertEqual(len(calls), 3)
        self.assertIn('--raw=/api/v1/namespaces/owned/pods?limit=17', calls[1].args[0])
        for call in calls:
            self.assertIn('--context=saved-context', call.args[0])
            self.assertIn('get', call.args[0])
            self.assertTrue(call.kwargs['check'])
            self.assertLessEqual(call.kwargs['timeout'], 7)

    def test_empty_selection_does_not_prove_cleanup(self):
        result, _ = self.collect([page([]), page([])])
        self.assertTrue(result['snapshot_complete'])
        self.assertEqual(result['namespaces'], [])
        self.assertFalse(result['cleanup_proved'])

    def test_namespace_replacement_or_disappearance_rejects_partial_snapshot(self):
        for after in [[namespace('replacement')], []]:
            with self.subTest(after=after):
                result, _ = self.collect([page([namespace()]), page([pod()]), page(after)])
                self.assertFalse(result['snapshot_complete'])
                self.assertNotIn('namespaces', result)

    def test_wrong_allocator_is_not_read(self):
        ns = namespace()
        ns['metadata']['labels'][observation.ALLOCATOR_LABEL] = 'other'
        result, run = self.collect([page([ns])])
        self.assertFalse(result['snapshot_complete'])
        self.assertEqual(run.call_count, 1)

    def test_wrong_pod_namespace_and_duplicate_identity_are_rejected(self):
        wrong = pod()
        wrong['metadata']['namespace'] = 'unrelated'
        for pods in [[wrong], [pod(), pod()]]:
            with self.subTest(pods=pods):
                result, _ = self.collect([page([namespace()]), page(pods)])
                self.assertFalse(result['snapshot_complete'])
                self.assertNotIn('namespaces', result)

    def test_page_limits_and_continuation_are_not_complete(self):
        for reply in [page([namespace()] * 17), page([], metadata={'continue': 'next'}),
                      SimpleNamespace(stdout=b' ' * (observation.MAX_RESPONSE + 1)),
                      SimpleNamespace(stdout=b'{}')]:
            with self.subTest(reply=len(reply.stdout)):
                result, _ = self.collect([reply])
                self.assertFalse(result['snapshot_complete'])

    def test_pod_list_failure_is_private_and_not_absence(self):
        error = subprocess.CalledProcessError(1, ['PRIVATE_DATA'], stderr=b'PRIVATE_DATA')
        result, _ = self.collect([page([namespace()]), error])
        self.assertFalse(result['snapshot_complete'])
        self.assertNotIn('namespaces', result)
        self.assertNotIn('PRIVATE_DATA', json.dumps(result))

    def test_invalid_fixture_cannot_change_request_path(self):
        with patch.object(observation.subprocess, 'run') as run:
            result = observation.collect('saved-context', '../unrelated')
        self.assertFalse(result['snapshot_complete'])
        run.assert_not_called()

    def test_overlong_and_malformed_selected_fields_fail_closed(self):
        for field in ['uid', 'name']:
            value = pod()
            value['metadata'][field] = 'x' * 257
            result, _ = self.collect([page([namespace()]), page([value])])
            self.assertFalse(result['snapshot_complete'])
        value = pod()
        value['status']['containerStatuses'][0]['state']['terminated']['exitCode'] = {'PRIVATE_DATA': 'value'}
        result, _ = self.collect([page([namespace()]), page([value])])
        self.assertFalse(result['snapshot_complete'])
        self.assertNotIn('PRIVATE_DATA', json.dumps(result))

    def test_total_deadline_stops_further_reads(self):
        with patch.object(observation.time, 'monotonic', side_effect=[0, 31]), \
                patch.object(observation.subprocess, 'run') as run:
            result = observation.collect('saved-context', 'fixture')
        self.assertFalse(result['snapshot_complete'])
        self.assertEqual(result['error_type'], 'TimeoutError')
        run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
