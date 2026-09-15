#!/usr/bin/env python3
"""Plan the public Gateway test permission update without cluster requests.

Use a saved operator installation and a fresh, frozen inspection render. The
plan is evidence for an operator update; it is not a Kubernetes apply document.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import uuid

NAMESPACE = 'stego-service-ci'
PREFIX = NAMESPACE + '.hypershell-namespace-allocation.'
MANIFESTS = ('hypershell', 'hypershell-console', 'hypershell-provisioner',
             'hypershell-namespace-allocation', 'hypershell-gateway-identity',
             'hypershell-gateway-workload')
VERSIONS = {'ClusterRole': 'rbac.authorization.k8s.io/v1',
            'ClusterRoleBinding': 'rbac.authorization.k8s.io/v1',
            'ValidatingAdmissionPolicy': 'admissionregistration.k8s.io/v1',
            'ValidatingAdmissionPolicyBinding': 'admissionregistration.k8s.io/v1'}
ADDITIONS = {
    PREFIX + 'fixture-gateway-inspector': {
        ('', 'secrets', 'get', 'openshell-public-tls'),
        ('cert-manager.io', 'certificates', 'get', 'openshell-public-tls'),
        ('cert-manager.io', 'certificates/status', 'update', 'openshell-public-tls')},
    PREFIX + 'gateway-worker': {
        ('route.openshift.io', 'routes', verb, None)
        for verb in ('create', 'delete', 'get', 'patch')},
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def strict_json(raw):
    require(isinstance(raw, str) and len(raw.encode()) <= 2 << 20, 'JSON exceeds the input limit')
    def fields(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, 'Repeated JSON field')
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=fields)


def read_text(path):
    with Path(path).open('rb') as source:
        raw = source.read((2 << 20) + 1)
    require(len(raw) <= 2 << 20, 'File exceeds the input limit')
    return raw.decode()


def read(path):
    return strict_json(read_text(path))


def digest(raw):
    return hashlib.sha256(raw.encode()).hexdigest()


def identity(value):
    require(isinstance(value, str) and str(uuid.UUID(value)) == value, 'Invalid resource UID')
    return value


def resources(manifests, hashes):
    require(set(manifests) == set(MANIFESTS) == set(hashes), 'Manifest inventory differs')
    found = {}
    for name in MANIFESTS:
        raw = manifests[name]
        require(digest(raw) == hashes[name], 'Manifest hash differs')
        value = strict_json(raw)
        require(set(value) == {'apiVersion', 'kind', 'items'} and value['apiVersion'] == 'v1'
                and value['kind'] == 'List' and isinstance(value['items'], list)
                and len(value['items']) <= 64, 'Invalid cluster manifest')
        for item in value['items']:
            require(isinstance(item, dict), 'Invalid cluster object')
            meta = item.get('metadata', {})
            kind = item.get('kind')
            require(kind in VERSIONS and item.get('apiVersion') == VERSIONS[kind]
                    and isinstance(meta, dict) and 'namespace' not in meta
                    and isinstance(meta.get('name'), str)
                    and re.fullmatch(re.escape(NAMESPACE) + r'\.[a-z0-9.-]+', meta['name']),
                    'Cluster resource is outside the fixture')
            key = kind, meta['name']
            require(key not in found, 'Repeated cluster resource')
            found[key] = item
    require(len(found) == 18, 'Require the existing eighteen cluster resources')
    return found


def grants(rules):
    require(isinstance(rules, list), 'Invalid role rules')
    result = set()
    for rule in rules:
        require(isinstance(rule, dict) and {'apiGroups', 'resources', 'verbs'} <= set(rule)
                and set(rule) <= {'apiGroups', 'resources', 'verbs', 'resourceNames'}, 'Unsupported role rule')
        for field in rule:
            require(isinstance(rule[field], list) and rule[field]
                    and all(isinstance(v, str) for v in rule[field]), 'Invalid role values')
        for group in rule['apiGroups']:
            for resource in rule['resources']:
                for verb in rule['verbs']:
                    for name in rule.get('resourceNames', [None]):
                        atom = group, resource, verb, name
                        require(atom not in result, 'Repeated role grant')
                        result.add(atom)
    return result


def plan(installation, rendered, manifests):
    require(installation.get('apiVersion') == 'v1' and installation.get('kind') == 'ConfigMap'
            and installation.get('immutable') is True, 'Require the immutable installation record')
    meta = installation['metadata']
    require(meta.get('name') == 'browser-ci-installation' and meta.get('namespace') == NAMESPACE
            and meta.get('labels', {}).get('app.kubernetes.io/managed-by') == 'stego-browser-ci'
            and not meta.get('deletionTimestamp') and not meta.get('finalizers')
            and not meta.get('ownerReferences'), 'Installation ownership differs')
    uid = identity(meta['uid'])
    require(isinstance(meta.get('resourceVersion'), str) and meta['resourceVersion'].isdigit(), 'Invalid installation revision')
    data = installation['data']
    require(set(data) == {name + '.json' for name in MANIFESTS} | {
        'namespace-uid', 'fs-group', 'issuer', 'kubernetes-endpoints.json',
        'kubernetes-service.json', 'cluster-installation.json'}, 'Installation fields differ')
    namespace_uid = identity(data['namespace-uid'])
    require(data['fs-group'].isdigit() and 0 < int(data['fs-group']) < 2**31, 'Invalid namespace file group')
    require(not installation.get('binaryData'), 'Unexpected binary installation data')
    old_record = strict_json(data['cluster-installation.json'])
    require(set(old_record) == {'namespace', 'source_sha256', 'manifests', 'resources', 'policy_type_checks'}
            and set(rendered) == {'namespace', 'source_sha256', 'manifests', 'resources', 'mode'},
            'Unexpected installation or render fields')
    require(old_record['source_sha256'].keys() == rendered['source_sha256'].keys()
            and 1 <= len(rendered['source_sha256']) <= 32, 'Renderer source inventory differs')
    for record in (old_record, rendered):
        for name, value in record['source_sha256'].items():
            require(re.fullmatch(r'(?:console/)?out/deploy/render/[a-zA-Z0-9_.-]+', name)
                    and isinstance(value, str) and re.fullmatch('[0-9a-f]{64}', value), 'Invalid renderer source hash')
    require(old_record['namespace'] == rendered['namespace'] == NAMESPACE
            and old_record.get('policy_type_checks') == 'success'
            and rendered.get('mode') == 'render-only' and rendered.get('resources') == [],
            'Require a checked installation and a read-only render')
    before = resources({name: data[name + '.json'] for name in MANIFESTS}, old_record['manifests'])
    after = resources(manifests, rendered['manifests'])
    require(before.keys() == after.keys(), 'An update cannot add or remove cluster resources')
    ids = {}
    for item in old_record['resources']:
        require(set(item) == {'kind', 'name', 'uid'}, 'Invalid installation journal item')
        key = item['kind'], item['name']
        require(key not in ids, 'Repeated installation resource')
        ids[key] = identity(item['uid'])
    require(ids.keys() == before.keys() and len(set(ids.values())) == len(ids), 'Installation journal inventory differs')
    changes = []
    for key in sorted(before):
        old, new = before[key], after[key]
        if old == new:
            continue
        require(key[0] == 'ClusterRole' and key[1] in ADDITIONS, 'Unexpected cluster resource change')
        require(set(old) == set(new) == {'kind', 'apiVersion', 'metadata', 'rules'}, 'Unsupported ClusterRole fields')
        require({k: v for k, v in old.items() if k != 'rules'} ==
                {k: v for k, v in new.items() if k != 'rules'}, 'Role fields changed outside rules')
        prior, following = grants(old['rules']), grants(new['rules'])
        require(not prior - following and following - prior == ADDITIONS[key[1]], 'Permission changes exceed the public test scope')
        changes.append({'kind': key[0], 'name': key[1], 'uid': ids[key],
                        'before': old, 'after': new,
                        'added_grants': [list(atom) for atom in sorted(ADDITIONS[key[1]])]})
    require({change['name'] for change in changes} == set(ADDITIONS), 'Require both public permission changes')
    proposed = copy.deepcopy(data)
    proposed_record = copy.deepcopy(rendered)
    proposed_record.pop('mode')
    proposed_record['resources'] = copy.deepcopy(old_record['resources'])
    proposed_record['policy_type_checks'] = 'pending-live-verification'
    proposed['cluster-installation.json'] = json.dumps(proposed_record, indent=2) + '\n'
    for name, raw in manifests.items():
        proposed[name + '.json'] = raw
    return {'kind': 'BrowserPublicPermissionPlan', 'namespace': NAMESPACE,
            'namespace_uid': namespace_uid, 'installation_uid': uid,
            'installation_resource_version': meta['resourceVersion'],
            'installation_data_sha256': digest(json.dumps(data, sort_keys=True, separators=(',', ':'))),
            'changes': changes, 'unchanged_resource_count': len(before) - len(changes),
            'proposed_installation_data': proposed,
            'required_live_checks': [
                'Acquire and retain the shared live-test Lease.',
                'Require the same namespace and installation UID, revision, and data.',
                'Require no test workloads or allocated namespaces.',
                'Check all eighteen resource UIDs and complete specifications against the installation.',
                'Use fresh UID and resourceVersion tests for each Role patch.',
                'Verify all resulting resources and admission policy type checks.',
                'Replace the immutable record with UID and revision preconditions only after verification.',
                'Keep the original record and an update journal for failure recovery.',
                'Verify the new record before release of the Lease.']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--installation', required=True, type=Path)
    parser.add_argument('--rendered', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    manifests = {}
    for name in MANIFESTS:
        path = args.rendered / 'cluster-manifests' / (name + '.json')
        manifests[name] = read_text(path)
    result = plan(read(args.installation), read(args.rendered / 'cluster-installation.json'), manifests)
    descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as output:
        output.write(json.dumps(result, indent=2) + '\n')
    print('Planned two ClusterRole updates; sixteen cluster resources remain unchanged. No cluster requests were made.')


if __name__ == '__main__':
    main()
