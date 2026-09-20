"""Check the Pod status record's scope and failure handling."""
import importlib.util
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('startup', Path(__file__).with_name('collect-service-startup.py'))
startup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(startup)


class StartupEvidence(unittest.TestCase):
    def test_preempted_pod_keeps_disruption_and_exit_status(self):
        pod = {'metadata': {'uid': 'test-pod'}, 'status': {'phase': 'Failed',
            'reason': 'Preempted', 'message': 'SECRET',
            'conditions': [{'type': 'DisruptionTarget', 'status': 'True',
                'reason': 'PreemptionByScheduler', 'message': 'A higher-priority Pod needs this node'}],
            'containerStatuses': [{'name': 'test', 'state': {'terminated': {
                'reason': 'Error', 'exitCode': 137, 'signal': 9, 'message': 'SECRET'}}}]}}
        result = startup.summary(pod)
        self.assertEqual(result['uid'], 'test-pod')
        self.assertEqual(result['phase'], 'Failed')
        self.assertEqual(result['reason'], 'Preempted')
        self.assertEqual(result['conditions'][0]['reason'], 'PreemptionByScheduler')
        self.assertEqual(result['container_states'][0]['state']['terminated']['exitCode'], 137)
        self.assertNotIn('SECRET', json.dumps(result))

    def test_pending_pod_keeps_reason_and_limits_without_credentials(self):
        pod = {'metadata': {'name': 'service-check-one', 'uid': 'one', 'annotations': {'private': 'SECRET'}},
            'spec': {'containers': [{'name': 'test', 'env': [{'name': 'TOKEN', 'value': 'SECRET'}],
                'command': ['SECRET'], 'resources': {'requests': {'cpu': '500m'}}}]},
            'status': {'phase': 'Pending', 'conditions': [{'type': 'PodScheduled', 'status': 'False',
                'reason': 'Unschedulable', 'message': 'Insufficient cpu'}],
                'containerStatuses': [{'name': 'test', 'state': {'terminated': {'exitCode': 1, 'message': 'SECRET'}}}]}}
        result = startup.summary(pod)
        self.assertNotIn('SECRET', json.dumps(result))
        self.assertEqual(result['conditions'][0]['reason'], 'Unschedulable')
        self.assertEqual(result['containers'][0]['resources']['requests']['cpu'], '500m')
        self.assertEqual(result['container_states'][0]['state']['terminated']['exitCode'], 1)

    def test_reads_only_the_test_pod_with_explicit_context(self):
        with patch.object(startup.subprocess, 'run', return_value=SimpleNamespace(stdout=b'{"items": []}')) as run:
            result = startup.collect('saved-ci', 'test-namespace')
        self.assertTrue(result['complete'])
        words = run.call_args.args[0]
        self.assertIn('--context=saved-ci', words)
        self.assertIn('--raw=/api/v1/namespaces/test-namespace/pods?labelSelector=job-name%3Dservice-check&limit=2', words)
        self.assertEqual(run.call_args.kwargs['timeout'], 20)

    def test_read_failure_is_not_a_complete_record(self):
        failure = subprocess.CalledProcessError(1, ['oc'], stderr=b'SECRET')
        with patch.object(startup.subprocess, 'run', side_effect=failure):
            result = startup.collect('saved-ci', 'test-namespace')
        self.assertFalse(result['complete'])
        self.assertNotIn('SECRET', json.dumps(result))

    def test_rejects_large_or_incomplete_snapshots(self):
        for page in [{'items': [{}, {}, {}]}, {'metadata': {'continue': 'next'}, 'items': []}, {}]:
            with patch.object(startup.subprocess, 'run', return_value=SimpleNamespace(stdout=json.dumps(page).encode())):
                self.assertFalse(startup.collect('saved-ci', 'test-namespace')['complete'])


if __name__ == '__main__':
    unittest.main()
