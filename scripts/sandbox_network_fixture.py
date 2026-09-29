"""Limit the native network fixture to one declared RuntimeClass change.

This fixture tests packet rules. It does not test OpenShell or VM isolation.
The production declaration and all other Pod guards must remain unchanged.
The production Sandbox profile is classless; the test copy declares a fixed
native runtime class so the generated class guard exists and is exercised.
"""
import json
import re
from pathlib import Path

from gateway_endpoint_fixture import read_json, INSPECTION_RECORD_LIMIT

RUNTIME_CLASS = 'stego-ci-sandbox-network'
RUNTIME_HANDLER = 'crun'
EXPRESSION = 'has(object.spec.runtimeClassName) && object.spec.runtimeClassName == '


def enabled(source):
    path = Path(source) / 'acceptance/browser-inspection-source.json'
    if not path.exists():
        return False
    value = read_json(path, limit=INSPECTION_RECORD_LIMIT)
    if 'sandbox_network_probe' not in value:
        return False
    if value['sandbox_network_probe'] != record() or value.get('network_endpoint_change'):
        raise ValueError('The native network fixture record differs')
    return True


def declaration(source):
    if RUNTIME_CLASS in source:
        raise ValueError('The native network fixture is already present')
    matches = list(re.finditer(r'^      - name: sandbox\n.*?(?=^      - name: |^    workers:|\Z)', source, re.MULTILINE | re.DOTALL))
    if len(matches) != 1:
        raise ValueError('Require one Sandbox allocation profile')
    match = matches[0]
    block = match.group()
    if block.count('        pod_runtime_class: ') != 0 or block.count('        pod_security: isolated-runtime\n') != 1:
        raise ValueError('Require the classless isolated Sandbox profile')
    before = '        pod_security: isolated-runtime\n'
    after = before + '        pod_runtime_class: ' + RUNTIME_CLASS + '\n'
    changed = block.replace(before, after, 1)
    return source[:match.start()] + changed + source[match.end():]


def restore_manifest(raw):
    """Remove only the class guard before the existing strict render comparison."""
    document = json.loads(raw)
    policies = [item for item in document['items'] if item['kind'] == 'ValidatingAdmissionPolicy' and item['metadata']['name'].endswith('.hypershell-namespace-allocation.pods.sandbox')]
    if len(policies) != 1:
        raise ValueError('Require one generated Sandbox Pod policy')
    rules = policies[0]['spec']['validations']
    selected = [rule for rule in rules if rule['expression'] == EXPRESSION + json.dumps(RUNTIME_CLASS)]
    if len(selected) != 1:
        raise ValueError('The native runtime guard is missing or repeated')
    rules.remove(selected[0])
    return json.dumps(document).encode()


def record():
    return {'runtime_class': RUNTIME_CLASS, 'runtime_handler': RUNTIME_HANDLER,
            'production_runtime_class': None,
            'vm_isolation_tested': False,
            'scope': 'Native packet probes only. The classless production Sandbox profile gains one fixed runtime class in the test copy. All other Pod, account, namespace, quota, and network rules stay unchanged.'}
