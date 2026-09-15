#!/usr/bin/env python3
"""Make a frozen browser fixture with declared namespace inspection rights.

Production declarations and generated files stay unchanged. STEGO generates
both the allocator and its admission rules from the fixture declaration.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROLES = '''      - name: fixture-gateway-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [secrets], resource_names: [openshell-gateway-db-credentials, openshell-gateway-keys, openshell-server-tls], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
          - {api_group: "", resources: [pods], verbs: [get, list, watch, delete]}
          - {api_group: apps, resources: [deployments], resource_names: [openshell-gateway], verbs: [get, list, watch]}
      - name: fixture-state-inspector
        scope: namespace
        rules:
          - {api_group: "", resources: [secrets], resource_names: [openshell-gateway-state], verbs: [get]}
          - {api_group: "", resources: [resourcequotas], resource_names: [stego-allocation], verbs: [get]}
'''
BINDINGS = [
    ('          - {role: gateway-worker, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-gateway-inspector, service_account: service-check, namespace: control}\n'),
    ('          - {role: gateway-state, service_account: hypershell-gateway-workload, namespace: control}\n',
     '          - {role: fixture-state-inspector, service_account: service-check, namespace: control}\n'),
]

def declaration(source):
    if 'fixture-gateway-inspector' in source or 'fixture-state-inspector' in source:
        raise ValueError('The source already contains fixture inspection roles')
    for boundary in ('      - name: gateway-state\n        identity_config_map:', '    workers:\n'):
        if source.count(boundary) != 1:
            raise ValueError('The production profile boundary changed')
    anchors = [('    allocation_roles:\n', ROLES), *BINDINGS]
    result = source
    for anchor, addition in anchors:
        if result.count(anchor) != 1:
            raise ValueError('The production allocation declaration changed')
        result = result.replace(anchor, anchor + addition, 1)
    # The two added bindings must be last, so all production binding indices
    # and resource names stay unchanged. Move each to its profile's end.
    for _, addition in BINDINGS:
        result = result.replace(addition, '', 1)
    result = result.replace('      - name: gateway-state\n        identity_config_map:', BINDINGS[0][1] + '      - name: gateway-state\n        identity_config_map:', 1)
    result = result.replace('    workers:\n', BINDINGS[1][1] + '    workers:\n', 1)
    restored = result.replace(ROLES, '', 1)
    for _, addition in BINDINGS:
        if result.count(addition) != 1:
            raise ValueError('The fixture binding placement is ambiguous')
        restored = restored.replace(addition, '', 1)
    if restored != source:
        raise ValueError('The fixture changed the production declaration')
    return result


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
    return [
        {'Name': 'fixture-gateway-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'secrets', ['get'], ['openshell-gateway-db-credentials', 'openshell-gateway-keys', 'openshell-server-tls']),
            quota, rule('', 'pods', ['delete', 'get', 'list', 'watch']),
            rule('apps', 'deployments', ['get', 'list', 'watch'], ['openshell-gateway'])]},
        {'Name': 'fixture-state-inspector', 'Scope': 'namespace', 'Rules': [
            rule('', 'secrets', ['get'], ['openshell-gateway-state']), quota]},
    ]


def verify_runtime(before, after):
    original, fixture = allocation_config(before), allocation_config(after)
    additions = {'fixture-gateway-inspector': 'gateway', 'fixture-state-inspector': 'gateway-state'}
    roles = [r for r in fixture['Roles'] if r['Name'] in additions]
    if roles != inspection_roles():
        raise ValueError('The generated inspection permissions differ from the fixed contract')
    fixture['Roles'] = [r for r in fixture['Roles'] if r['Name'] not in additions]
    for role, name in additions.items():
        profiles = [p for p in fixture['Profiles'] if p['Name'] == name]
        if len(profiles) != 1:
            raise ValueError('The inspection profile is missing')
        binding = profiles[0]['Bindings'].pop()
        if binding != {'Role': role, 'ExternalRole': '', 'ServiceAccount': 'service-check', 'Namespace': 'control', 'ExternalNamespace': ''}:
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
    # Only the allocator's allowed RoleBinding cases can change. Ownership,
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


def check_render(source, destination, env):
    with tempfile.TemporaryDirectory(prefix='stego-inspection-render-') as directory:
        renders = []
        for index, root in enumerate((source, destination)):
            binary = str(Path(directory) / str(index))
            subprocess.run(['go', 'build', '-p=1', '-mod=readonly', '-trimpath', '-o', binary, './out/deploy/render'],
                           cwd=root, env=env, check=True, timeout=45)
            renders.append(subprocess.check_output([binary, '--namespace', 'stego-service-inspection', '--fs-group', '10001',
                '--image', 'registry.example.test/fixture@sha256:' + 'a' * 64, '--worker', 'namespace-allocation',
                '--egress', 'kubernetes=192.0.2.1:443'], env=env, timeout=5))
        verify_manifests(*renders)
        return {'production_sha256': hashlib.sha256(renders[0]).hexdigest(),
                'fixture_sha256': hashlib.sha256(renders[1]).hexdigest(),
                'scope': 'Two namespace roles, allocator bind names, and declared namespace binding cases only.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compiler', required=True, type=Path)
    parser.add_argument('--destination', required=True, type=Path)
    args = parser.parse_args()
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
    config.write_text(declaration(config.read_text()))
    env = dict(os.environ, GOMAXPROCS='1', GOMEMLIMIT='256MiB', GOWORK='off')
    for key in ('STEGO_REGISTRY', 'STEGO_MODULE', 'STEGO_GO_VERSION'):
        env.pop(key, None)
    subprocess.run([str(compiler), 'apply'], cwd=destination, env=env, check=True, timeout=60)
    subprocess.run([str(compiler), 'drift'], cwd=destination, env=env, check=True, timeout=30)
    changed = {name for name, digest in hashes.items() if hashlib.sha256((destination / name).read_bytes()).hexdigest() != digest}
    allowed = {'service.yaml', '.stego/state.yaml', 'out/deploy/allocation/allocation.go', 'out/deploy/render/worker-namespace-allocation.json.tmpl'}
    if changed != allowed:
        raise ValueError('Unexpected fixture output changes: ' + ', '.join(sorted(changed)))
    runtime = 'out/deploy/allocation/allocation.go'
    roles = verify_runtime((source / runtime).read_text(), (destination / runtime).read_text())
    render = check_render(source, destination, env)
    # Files that appear during generation also need an explicit review.
    observed = {str(p.relative_to(destination)) for p in destination.rglob('*') if p.is_file()}
    if observed - {'.stego/apply.lock'} != set(hashes):
        raise ValueError('The fixture generator added or removed files')
    record = {'compiler_revision': revision, 'source_base_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip(),
              'source_sha256': hashes, 'fixture_sha256': {name: hashlib.sha256((destination / name).read_bytes()).hexdigest() for name in hashes},
              'changed_files': sorted(changed), 'inspection_roles': roles, 'render': render,
              'scope': 'Production source with two namespace inspection roles and two appended bindings. Production binding indices and runtime code are unchanged.'}
    (destination / 'acceptance/browser-inspection-source.json').write_text(json.dumps(record, indent=2) + '\n')
    print('Prepared frozen inspection fixture: ' + str(destination))


if __name__ == '__main__':
    main()
