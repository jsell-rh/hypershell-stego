#!/usr/bin/env python3
"""Make a frozen browser fixture with declared namespace inspection rights.

Production declarations and generated files stay unchanged. STEGO generates
both the allocator and its admission rules from the fixture declaration.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

from gateway_endpoint_fixture import INSPECTION_RECORD_LIMIT


def write_inspection_record(path, record):
    data = (json.dumps(record, indent=2) + "\n").encode("utf-8")
    if len(data) > INSPECTION_RECORD_LIMIT:
        raise ValueError("The source inspection record exceeds its limit")
    Path(path).write_bytes(data)


ROLES = '''      - name: fixture-gateway-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [configmaps], resource_names: [openshell-gateway-config], verbs: [get]}
          - {api_group: "", resources: [secrets], resource_names: [openshell-client-tls, openshell-gateway-db-credentials, openshell-gateway-keys, openshell-public-tls, openshell-server-tls, hypershell-gateway-console-files], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], verbs: [list]}
          - {api_group: "", resources: [pods], verbs: [get, list, watch, create, delete]}
          - {api_group: "", resources: [pods/log], verbs: [get]}
          - {api_group: "", resources: [serviceaccounts], verbs: [get]}
          - {api_group: apps, resources: [deployments], resource_names: [hypershell-gateway-console, openshell-gateway], verbs: [get, list, watch]}
          - {api_group: cert-manager.io, resources: [certificates], resource_names: [openshell-public-tls], verbs: [get]}
          - {api_group: cert-manager.io, resources: [certificates/status], resource_names: [openshell-public-tls], verbs: [update]}
      - name: fixture-state-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [secrets], resource_names: [openshell-gateway-state], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], verbs: [list]}
      - name: fixture-console-state-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [secrets], resource_names: [gateway-console-state], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], verbs: [list]}
      - name: fixture-sandbox-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [secrets], resource_names: [openshell-client-tls], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: networking.k8s.io, resources: [networkpolicies], verbs: [list]}
          - {api_group: "", resources: [pods], verbs: [get, list, watch, create, delete]}
          - {api_group: "", resources: [pods/log], verbs: [get]}
          - {api_group: "", resources: [serviceaccounts], verbs: [get]}
          - {api_group: rbac.authorization.k8s.io, resources: [rolebindings], verbs: [get, list]}
'''
BINDINGS = [
    ('          - {role: gateway-worker, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-gateway-inspector, service_account: service-check, namespace: control}\n'),
    ('          - {role: gateway-state, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-state-inspector, service_account: service-check, namespace: control}\n'),
    ('          - {role: gateway-state, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-console-state-inspector, service_account: service-check, namespace: control}\n'),
    ('          - {role: sandbox-worker, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-sandbox-inspector, service_account: service-check, namespace: control}\n'),
]

def declaration(source):
    if any(role in source for role in ('fixture-gateway-inspector', 'fixture-state-inspector', 'fixture-console-state-inspector', 'fixture-sandbox-inspector')):
        raise ValueError('The source already contains fixture inspection roles')
    if source.count('    allocation_roles:\n') != 1 or source.count('    allocation_profiles:\n') != 1 or source.count('    workers:\n') != 1:
        raise ValueError('The production profile boundary changed')
    before, profiles = source.split('    allocation_profiles:\n', 1)
    profiles, after = profiles.split('    workers:\n', 1)
    # Append each inspection binding inside its own profile. Other state
    # profiles keep their existing permissions and binding indices.
    for name, (anchor, addition) in zip(('gateway', 'gateway-state', 'gateway-console-state', 'sandbox'), BINDINGS, strict=True):
        pattern = r'^      - name: ' + re.escape(name) + r'\n.*?(?=^      - name: |\Z)'
        matches = list(re.finditer(pattern, profiles, re.MULTILINE | re.DOTALL))
        if len(matches) != 1 or matches[0].group().count(anchor) != 1:
            raise ValueError('The production allocation declaration changed')
        block = matches[0]
        profiles = profiles[:block.end()] + addition + profiles[block.end():]
    result = before + '    allocation_profiles:\n' + profiles + '    workers:\n' + after
    result = result.replace('    allocation_roles:\n', '    allocation_roles:\n' + ROLES, 1)
    restored = result.replace(ROLES, '', 1)
    for _, addition in BINDINGS:
        if result.count(addition) != 1:
            raise ValueError('The fixture binding placement is ambiguous')
        restored = restored.replace(addition, '', 1)
    if restored != source:
        raise ValueError('The fixture changed the production declaration')
    return result


def endpoint_change_declaration(source):
    if source.count('    allocation_profiles:\n') != 1 or source.count('    workers:\n') != 1 or 'network-probe' in source:
        raise ValueError('The Gateway endpoint test boundary differs')
    before, profiles = source.split('    allocation_profiles:\n', 1)
    profiles, after = profiles.split('    workers:\n', 1)
    matches = list(re.finditer(r'^      - name: gateway\n.*?(?=^      - name: |\Z)', profiles, re.MULTILINE | re.DOTALL))
    if len(matches) != 1:
        raise ValueError('The Gateway endpoint profile is missing or repeated')
    match = matches[0]
    block = match.group()
    endpoint = '        network_endpoints: [kubernetes]\n'
    if block.count(endpoint) != 1 or block.count('        network_isolation: true\n') != 1:
        raise ValueError('The Gateway endpoint test requires one isolated endpoint')
    changed = block.replace(endpoint, endpoint.replace('[kubernetes]', '[kubernetes, network-probe]'), 1)
    profiles = profiles[:match.start()] + changed + profiles[match.end():]
    return before + '    allocation_profiles:\n' + profiles + '    workers:\n' + after


def verify_endpoint_change_runtime(before, after):
    original, fixture = allocation_config(before), allocation_config(after)
    profiles = [p for p in fixture['Profiles'] if p['Name'] == 'gateway']
    if len(profiles) != 1 or profiles[0]['NetworkEndpoints'] != ['kubernetes', 'network-probe']:
        raise ValueError('The endpoint test must add one exact Gateway endpoint name')
    profiles[0]['NetworkEndpoints'].pop()
    pattern = r'json.Unmarshal\(\[\]byte\(("(?:[^"\\]|\\.)*")\), &config\)'
    if original != fixture or re.sub(pattern, 'CONFIG', before) != re.sub(pattern, 'CONFIG', after):
        raise ValueError('The endpoint test changed another allocation field or runtime code')


def verify_endpoint_worker_template(before, after):
    original = json.dumps(json.dumps({'gateway': ['kubernetes']}, separators=(',', ':')))
    changed = json.dumps(json.dumps({'gateway': ['kubernetes', 'network-probe']}, separators=(',', ':')))
    if before.count(original) != 1 or after != before.replace(original, changed, 1):
        raise ValueError('The endpoint test changed more than the worker allocation annotation')


def allocation_config(source):
    match = re.search(r'json.Unmarshal\(\[\]byte\(("(?:[^"\\]|\\.)*")\), &config\)', source)
    if not match:
        raise ValueError('The generated allocation configuration is missing')
    return json.loads(json.loads(match[1]))


def inspection_roles():
    def rule(group, resource, verbs, names=None):
        value = {'apiGroups': [group], 'resources': [resource], 'verbs': verbs}
        if names is not None:
            value['resourceNames'] = names
        return value
    quota = rule('', 'resourcequotas', ['get'], ['stego-allocation'])
    network = [rule('networking.k8s.io', 'networkpolicies', ['get'], ['stego-allocation']), rule('networking.k8s.io', 'networkpolicies', ['list'])]
    return [
        {'Name': 'fixture-gateway-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'configmaps', ['get'], ['openshell-gateway-config']),
            rule('', 'secrets', ['get'], ['hypershell-gateway-console-files', 'openshell-client-tls', 'openshell-gateway-db-credentials', 'openshell-gateway-keys', 'openshell-public-tls', 'openshell-server-tls']),
            quota, *network, rule('', 'pods', ['create', 'delete', 'get', 'list', 'watch']), rule('', 'pods/log', ['get']),
            rule('', 'serviceaccounts', ['get']),
            rule('apps', 'deployments', ['get', 'list', 'watch'], ['hypershell-gateway-console', 'openshell-gateway']),
            rule('cert-manager.io', 'certificates', ['get'], ['openshell-public-tls']),
            rule('cert-manager.io', 'certificates/status', ['update'], ['openshell-public-tls'])]},
        {'Name': 'fixture-state-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'secrets', ['get'], ['openshell-gateway-state']), quota, *network]},
        {'Name': 'fixture-console-state-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'secrets', ['get'], ['gateway-console-state']), quota, *network]},
        {'Name': 'fixture-sandbox-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'secrets', ['get'], ['openshell-client-tls']),
            quota, *network, rule('', 'pods', ['create', 'delete', 'get', 'list', 'watch']),
            rule('', 'pods/log', ['get']), rule('', 'serviceaccounts', ['get']),
            rule('rbac.authorization.k8s.io', 'rolebindings', ['get', 'list'])]},
    ]


def verify_runtime(before, after):
    original, fixture = allocation_config(before), allocation_config(after)
    additions = {'fixture-gateway-inspector': 'gateway', 'fixture-state-inspector': 'gateway-state', 'fixture-console-state-inspector': 'gateway-console-state', 'fixture-sandbox-inspector': 'sandbox'}
    roles = [r for r in fixture['Roles'] if r['Name'] in additions]
    if roles != inspection_roles():
        raise ValueError('The generated inspection permissions differ from the fixed contract')
    fixture['Roles'] = [r for r in fixture['Roles'] if r['Name'] not in additions]
    for role, name in additions.items():
        profiles = [p for p in fixture['Profiles'] if p['Name'] == name]
        if len(profiles) != 1:
            raise ValueError('The inspection profile is missing')
        binding = profiles[0]['Bindings'].pop()
        if binding != {'Role': role, 'ExternalRole': '', 'ServiceAccount': 'service-check', 'Namespace': 'control', 'ExternalNamespace': '', 'SubjectProfile': '', 'SubjectPrefix': ''}:
            raise ValueError('The fixture changed a production binding')
    if original != fixture:
        raise ValueError('The fixture changed the production allocation configuration')
    # The executable runtime must be byte-identical except for its configuration.
    pattern = r'json.Unmarshal\(\[\]byte\(("(?:[^"\\]|\\.)*")\), &config\)'
    if re.sub(pattern, 'CONFIG', before) != re.sub(pattern, 'CONFIG', after):
        raise ValueError('The fixture changed the allocation runtime')
    return roles


def verify_manifests(before, after):
    def objects(data):
        document = json.loads(data)
        items = {(item['kind'], item['metadata']['name']): item for item in document['items']}
        if len(items) != len(document['items']):
            raise ValueError('The rendered manifest has duplicate identities')
        return items
    original, fixture = objects(before), objects(after)
    base = 'stego-service-inspection.hypershell-namespace-allocation'
    added = {('ClusterRole', base + '.' + r['Name']) for r in inspection_roles()}
    if fixture.keys() - original.keys() != added or original.keys() - fixture.keys():
        raise ValueError('The fixture changed the rendered resource set')
    for role in inspection_roles():
        item = fixture.pop(('ClusterRole', base + '.' + role['Name']))
        if item != {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRole',
                    'metadata': {'name': base + '.' + role['Name']}, 'rules': role['Rules']}:
            raise ValueError('The rendered inspection role differs')
    for rule in fixture[('ClusterRole', base)]['rules']:
        if rule.get('verbs') == ['bind']:
            for role in inspection_roles():
                rule['resourceNames'].remove(base + '.' + role['Name'])
    # The fixture adds service-check to the reserved control names. Require
    # both exact name lists to change, with every other guard field preserved.
    guard_key = ('ValidatingAdmissionPolicy', base + '.control-accounts')
    if guard_key not in original or guard_key not in fixture:
        raise ValueError('The control account guard is missing')
    expected_guard = copy.deepcopy(original[guard_key])
    validations = expected_guard['spec']['validations']
    if len(validations) != 1:
        raise ValueError('The control account guard has an unexpected rule set')
    names = ['hypershell-gateway-workload', 'hypershell-namespace-allocation', 'hypershell-sandbox-count']
    before_names = json.dumps(names, separators=(',', ':'))
    after_names = json.dumps(names + ['service-check'], separators=(',', ':'))
    expression = validations[0]['expression']
    if expression.count(before_names) != 2:
        raise ValueError('The control account name rules differ')
    validations[0]['expression'] = expression.replace(before_names, after_names)
    if fixture[guard_key] != expected_guard:
        raise ValueError('The fixture changed the control account guard beyond its reserved name')
    fixture[guard_key] = original[guard_key]
    # Only the allocator's allowed RoleBinding cases can also change. Ownership,
    # quotas, cluster bindings, and all namespace objects must remain equal.
    key = ('ValidatingAdmissionPolicy', base + '.allocation')
    for objects in (original, fixture):
        matches = 0
        for validation in objects[key]['spec']['validations']:
            if validation['expression'].startswith("request.resource.resource != 'rolebindings' || "):
                validation['expression'] = 'DECLARED_NAMESPACE_BINDINGS'
                matches += 1
        if matches != 1:
            raise ValueError('The allocation binding rule is missing or repeated')
    if original != fixture:
        raise ValueError('The fixture changed unrelated deployment or admission rules')


def check_render(source, destination, env, network_baseline=None, endpoint_change=False, sandbox_network=False):
    with tempfile.TemporaryDirectory(prefix='stego-inspection-render-') as directory:
        renders = []
        binaries = []
        for index, root in enumerate((network_baseline or source, destination)):
            binary = str(Path(directory) / str(index))
            binaries.append(binary)
            subprocess.run(['go', 'build', '-p=1', '-mod=readonly', '-trimpath', '-o', binary, './out/deploy/render'],
                           cwd=root, env=env, check=True, timeout=45)
            flags = ['--egress', 'network-probe=192.0.2.3:8080'] if endpoint_change else []
            renders.append(subprocess.check_output([binary, '--namespace', 'stego-service-inspection', '--fs-group', '10001',
                '--image', 'registry.example.test/fixture@sha256:' + 'a' * 64, '--worker', 'namespace-allocation',
                '--egress', 'kubernetes=192.0.2.1:443', *flags], env=env, timeout=5))
        if sandbox_network:
            from sandbox_network_fixture import restore_manifest
            verify_manifests(renders[0], restore_manifest(renders[1]))
        else:
            verify_manifests(*renders)
        record = {'production_sha256': hashlib.sha256(renders[0]).hexdigest(),
                  'fixture_sha256': hashlib.sha256(renders[1]).hexdigest(),
                  'scope': 'Four namespace roles, allocator bind names, declared namespace binding cases, and the reserved fixture control account only.'}
        if sandbox_network:
            record['scope'] = record['scope'].removesuffix(' only.') + ', and the fixed test RuntimeClass guard only.'
        if endpoint_change:
            from gateway_endpoint_fixture import policy_change
            transitions = []
            for endpoint in ('192.0.2.3:8080', '192.0.2.4:8080'):
                transitions.append(subprocess.check_output([binaries[1], '--namespace', 'stego-service-inspection',
                    '--fs-group', '10001', '--image', 'registry.example.test/fixture@sha256:' + 'a' * 64,
                    '--worker', 'namespace-allocation', '--scope', 'cluster',
                    '--egress', 'kubernetes=192.0.2.1:443', '--egress', 'network-probe=' + endpoint], env=env, timeout=5))
            _, changed = policy_change(*(json.loads(data) for data in transitions), '192.0.2.3:8080', '192.0.2.4:8080')
            record['endpoint_transition'] = {'kind': changed['kind'], 'name': changed['metadata']['name'],
                'initial_sha256': hashlib.sha256(transitions[0]).hexdigest(),
                'replacement_sha256': hashlib.sha256(transitions[1]).hexdigest(),
                'scope': 'One address replacement in the Gateway admission variable. All other cluster fields remain equal.',
                'live_traffic_verified': False}
        return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compiler', required=True, type=Path)
    parser.add_argument('--destination', required=True, type=Path)
    parser.add_argument('--network-endpoint-change', action='store_true', help='Add one Gateway endpoint for the direct address-change test')
    parser.add_argument('--sandbox-network', action='store_true', help='Use the fixed native runtime only for Sandbox packet probes')
    args = parser.parse_args()
    if args.sandbox_network and args.network_endpoint_change:
        parser.error('Select only one network fixture mode')
    source = Path(__file__).resolve().parent.parent
    destination = args.destination.resolve()
    compiler = args.compiler.resolve()
    if destination.exists() or destination == source or source in destination.parents:
        raise ValueError('Use a new fixture directory outside the repository')
    revision = (source / '.stego/compiler-revision').read_text().strip()
    build = subprocess.check_output(['go', 'version', '-m', str(compiler)], text=True, timeout=10)
    if not re.fullmatch('[0-9a-f]{40}', revision) or 'vcs.revision=' + revision + '\n' not in build or 'vcs.modified=false\n' not in build:
        raise ValueError('Require a clean compiler build at the pinned revision')
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=source).decode().split('\0')
    hashes = {}
    for name in sorted(set(names)):
        if not name or name.startswith('.claude/'):
            continue
        path = source / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('The fixture source must contain regular files')
        target = destination / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, target)
        hashes[name] = hashlib.sha256(path.read_bytes()).hexdigest()
    config = destination / 'service.yaml'
    declared = declaration(config.read_text())
    if args.network_endpoint_change:
        declared = endpoint_change_declaration(declared)
    if args.sandbox_network:
        from sandbox_network_fixture import declaration as native_declaration
        declared = native_declaration(declared)
    config.write_text(declared)
    env = dict(os.environ, GOMAXPROCS='1', GOMEMLIMIT='256MiB', GOWORK='off')
    for key in ('STEGO_REGISTRY', 'STEGO_MODULE', 'STEGO_GO_VERSION'):
        env.pop(key, None)
    subprocess.run([str(compiler), 'apply'], cwd=destination, env=env, check=True, timeout=60)
    subprocess.run([str(compiler), 'drift'], cwd=destination, env=env, check=True, timeout=30)
    changed = {name for name, digest in hashes.items() if hashlib.sha256((destination / name).read_bytes()).hexdigest() != digest}
    allowed = {'service.yaml', '.stego/state.yaml', 'out/deploy/allocation/allocation.go', 'out/deploy/render/worker-namespace-allocation.json.tmpl'}
    if args.network_endpoint_change:
        allowed.add('out/deploy/render/worker-gateway-workload.json.tmpl')
    if args.network_endpoint_change:
        allowed.add('out/deploy/render/worker-sandbox-count.json.tmpl')
    if changed != allowed:
        raise ValueError('Unexpected fixture output changes: ' + ', '.join(sorted(changed)))
    runtime = 'out/deploy/allocation/allocation.go'
    with tempfile.TemporaryDirectory(prefix='stego-network-baseline-') as directory:
        baseline = source
        if args.network_endpoint_change:
            # Generate the network-only baseline with the pinned compiler.
            # Then compare the inspection roles against that exact baseline.
            baseline = Path(directory)
            for name in hashes:
                target = baseline / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source / name, target)
            declared = (source / 'service.yaml').read_text()
            declared = endpoint_change_declaration(declared)
            (baseline / 'service.yaml').write_text(declared)
            subprocess.run([str(compiler), 'apply'], cwd=baseline, env=env, check=True, timeout=60)
            verify_endpoint_change_runtime((source / runtime).read_text(), (baseline / runtime).read_text())
            for worker in ('gateway-workload', 'sandbox-count'):
                name = 'out/deploy/render/worker-' + worker + '.json.tmpl'
                verify_endpoint_worker_template((source / name).read_text(), (baseline / name).read_text())
                if (baseline / name).read_bytes() != (destination / name).read_bytes():
                    raise ValueError('The inspection roles changed an unrelated worker template')
        roles = verify_runtime((baseline / runtime).read_text(), (destination / runtime).read_text())
        render = check_render(source, destination, env, baseline, args.network_endpoint_change, args.sandbox_network)

    # Files that appear during generation also need an explicit review.
    observed = {str(p.relative_to(destination)) for p in destination.rglob('*') if p.is_file()}
    if observed - {'.stego/apply.lock'} != set(hashes):
        raise ValueError('The fixture generator added or removed files')
    record = {'compiler_revision': revision, 'source_base_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip(),
              'source_sha256': hashes, 'fixture_sha256': {name: hashlib.sha256((destination / name).read_bytes()).hexdigest() for name in hashes},
              'changed_files': sorted(changed), 'inspection_roles': roles, 'render': render,
              'scope': 'Production source with four namespace inspection roles and four appended bindings. Production binding indices and runtime code are unchanged.'}
    if args.network_endpoint_change:
        record['network_endpoint_change'] = {'endpoint': 'network-probe', 'scope': 'One operator-bound endpoint name in the Gateway allocation profile; no added Kubernetes permission.'}
    if args.sandbox_network:
        from sandbox_network_fixture import record as native_record
        record['sandbox_network_probe'] = native_record()
        record['scope'] += ' The test copy also selects the fixed native runtime for packet probes; it does not test VM isolation.'
    write_inspection_record(destination / 'acceptance/browser-inspection-source.json', record)
    print('Prepared frozen inspection fixture: ' + str(destination))


if __name__ == '__main__':
    main()
