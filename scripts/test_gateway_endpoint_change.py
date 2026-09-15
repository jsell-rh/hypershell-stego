"""Check conditional policy writes and recovery from a lost API response."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('transition', Path(__file__).with_name('change-gateway-endpoint.py'))
transition = importlib.util.module_from_spec(spec)
spec.loader.exec_module(transition)


class TransitionBoundary(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.change = object.__new__(transition.Change)
        c = self.change
        c.path = Path(self.temp.name) / 'journal.json'
        c.namespace, c.pod, c.policy, c.uid = 'stego-service-20260915-abcdef', 'service-check-abcde', 'test.allocation', 'policy-uid'
        c.input = {'nonce': 'a' * 32}
        self.before = {'apiVersion': 'admissionregistration.k8s.io/v1', 'kind': 'ValidatingAdmissionPolicy',
            'metadata': {'name': c.policy}, 'spec': {'failurePolicy': 'Fail', 'matchConstraints': {'resourceRules': []},
            'variables': [{'name': 'networkEndpoints', 'expression': '{"gateway":[{"cidr":"192.0.2.10/32","port":"8080"}]}'}]}}
        self.after = copy.deepcopy(self.before)
        self.after['spec']['variables'][0]['expression'] = self.after['spec']['variables'][0]['expression'].replace('192.0.2.10', '192.0.2.11')
        c.plan = {'before': self.before, 'after': self.after}
        c.replacement = b'fixed operator manifest'
        self.current = self.live(self.before, 1)
        self.calls, self.guards = [], []
        self.lose_patch_response = False
        self.warnings, self.stale = [], False
        c.object = lambda *args: copy.deepcopy(self.current)
        c.guard = lambda record: self.guards.append(copy.deepcopy(record))
        c.request = self.request
        c.save({'phase': 'prepared', 'control_uid': 'control-uid', 'pod_uid': 'pod-uid', 'policy_uid': c.uid, 'nonce': c.input['nonce']})

    def live(self, template, generation):
        value = copy.deepcopy(template)
        value['metadata'].update(uid=self.change.uid, resourceVersion=str(generation), generation=generation)
        value['status'] = {'observedGeneration': generation, 'typeChecking': {'expressionWarnings': []}}
        return value

    def request(self, *words, body=None):
        self.calls.append((words, body))
        if words[0] == 'patch':
            patch = json.loads(words[words.index('-p') + 1])
            self.assertEqual(patch[:3], [
                {'op': 'test', 'path': '/metadata/uid', 'value': self.change.uid},
                {'op': 'test', 'path': '/metadata/resourceVersion', 'value': '1'},
                {'op': 'test', 'path': '/spec', 'value': self.current['spec']}])
            self.current = self.live(self.after, 2)
            self.current['status']['typeChecking']['expressionWarnings'] = self.warnings
            if self.stale:
                self.current['status']['observedGeneration'] = 1
            if self.lose_patch_response:
                self.lose_patch_response = False
                raise transition.ObservationError('response lost after the write')
            return json.dumps(self.current).encode()
        if 'head -c 1025' in words[-1]:
            return json.dumps({'nonce': self.change.input['nonce'], 'action': 'replace'}).encode()
        self.assertIsNotNone(body)
        return b''

    def journal(self):
        return json.loads(self.change.path.read_text())

    def test_patch_has_identity_version_and_original_spec_conditions(self):
        self.change.step()
        self.assertEqual(self.journal()['phase'], 'complete')
        self.assertEqual(self.journal()['type_checks'], 'passed')
        self.assertEqual(self.journal()['generation'], 2)
        self.assertEqual(len(self.guards), 1)
        self.assertEqual(len([c for c, _ in self.calls if c[0] == 'patch']), 1)
        delivered = [body for _, body in self.calls if body is not None]
        self.assertEqual(delivered[0], self.change.replacement)
        self.assertEqual(json.loads(delivered[1])['policy_uid'], self.change.uid)

    def test_lost_write_response_is_read_and_not_blindly_repeated(self):
        self.lose_patch_response = True
        with self.assertRaises(transition.ObservationError):
            self.change.step()
        self.assertEqual(self.journal()['phase'], 'patching')
        self.change.step()
        self.assertEqual(self.journal()['phase'], 'complete')
        self.assertEqual(len([c for c, _ in self.calls if c[0] == 'patch']), 1)

    def test_stale_type_check_cannot_acknowledge_the_change(self):
        self.stale = True
        self.change.step()
        self.assertEqual(self.journal()['phase'], 'checking')
        self.assertFalse(any(body is not None for _, body in self.calls))
        self.current['status']['observedGeneration'] = 2
        self.change.step()
        self.assertEqual(self.journal()['phase'], 'complete')

    def test_type_warnings_prevent_manifest_and_acknowledgement_delivery(self):
        self.warnings = [{'warning': 'invalid field'}]
        with self.assertRaises(ValueError):
            self.change.step()
        self.assertFalse(any(body is not None for _, body in self.calls))

    def test_replaced_changed_or_early_transition_is_not_adopted(self):
        for modify in [lambda o: o['metadata'].update(uid='foreign'),
                       lambda o: o['spec'].update(failurePolicy='Ignore'),
                       lambda o: o.update(spec=copy.deepcopy(self.after['spec']))]:
            self.current = self.live(self.before, 1)
            modify(self.current)
            with self.assertRaises(ValueError):
                self.change.step()
        self.assertFalse(any(c[0] == 'patch' for c, _ in self.calls))

    def test_api_defaults_are_accepted_but_annotations_cannot_change(self):
        value = self.live(self.before, 1)
        value['spec']['matchConstraints'].update(matchPolicy='Equivalent', namespaceSelector={}, objectSelector={})
        transition.checked_patch(value, self.before, self.after, self.change.uid)
        value['metadata']['annotations'] = {'unexpected': 'value'}
        with self.assertRaises(ValueError):
            transition.checked_patch(value, self.before, self.after, self.change.uid)


if __name__ == '__main__':
    unittest.main()
