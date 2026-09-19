"""Limit the native network fixture to one declared RuntimeClass change.

This fixture tests packet rules. It does not test OpenShell or VM isolation.
The production declaration and all other Pod guards must remain unchanged.
"""
import json
import re
from pathlib import Path

from gateway_endpoint_fixture import read_json, INSPECTION_RECORD_LIMIT

RUNTIME_CLASS = 'stego-ci-sandbox-network'
RUNTIME_HANDLER = 'crun'
PRODUCTION_CLASS = 'kata'
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
    before = '        pod_runtime_class: ' + PRODUCTION_CLASS + '\n'
    if block.count(before) != 1 or block.count('        pod_security: isolated-runtime\n') != 1:
        raise ValueError('Require the isolated Sandbox profile with its production runtime')
    changed = block.replace(before, '        pod_runtime_class: ' + RUNTIME_CLASS + '\n', 1)
    return source[:match.start()] + changed + source[match.end():]


def restore_manifest(raw):
    """Restore only the class guard before the existing strict render comparison."""
    document = json.loads(raw)
    policies = [item for item in document['items'] if item['kind'] == 'ValidatingAdmissionPolicy' and item['metadata']['name'].endswith('.hypershell-namespace-allocation.pods.sandbox')]
    if len(policies) != 1:
        raise ValueError('Require one generated Sandbox Pod policy')
    rules = policies[0]['spec']['validations']
    selected = [rule for rule in rules if rule['expression'] == EXPRESSION + json.dumps(RUNTIME_CLASS)]
    if len(selected) != 1:
        raise ValueError('The native runtime guard is missing or repeated')
    selected[0]['expression'] = EXPRESSION + json.dumps(PRODUCTION_CLASS)
    return json.dumps(document).encode()


def record():
    return {'runtime_class': RUNTIME_CLASS, 'runtime_handler': RUNTIME_HANDLER,
            'production_runtime_class': PRODUCTION_CLASS,
            'vm_isolation_tested': False,
            'scope': 'Native packet probes only. The exact runtime class guard changes in the test copy. All other Pod, account, namespace, quota, and network rules stay unchanged.'}
