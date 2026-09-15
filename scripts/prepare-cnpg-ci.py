#!/usr/bin/env python3
"""Install fixed CNPG CI policy with the operator context. CI cannot change it."""
import sys
sys.dont_write_bytecode = True
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import time

import cnpg_ci as ci


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--storage-class', default='gp3-csi')
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=root, timeout=10).strip():
        raise RuntimeError('Commit the installation source before running it')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True, timeout=10).strip()
    args.results.mkdir(mode=0o700, parents=False, exist_ok=False)
    os.umask(0o077)
    record = {'source_revision': revision, 'installation_id': secrets.token_hex(16), 'complete': False, 'resources': []}
    def save():
        temporary = args.results / 'installation.tmp'
        temporary.write_text(json.dumps(record, indent=2) + '\n')
        temporary.replace(args.results / 'installation.json')
    def oc(*words, data=None):
        result = subprocess.run(['oc', '--context=' + args.context, '--request-timeout=20s', *words],
                                input=None if data is None else json.dumps(data), text=True, capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError('CNPG policy installation request failed: ' + words[0])
        return json.loads(result.stdout) if result.stdout.strip() else None
    def get(obj):
        words = ['get', obj['kind'], obj['metadata']['name'], '--ignore-not-found', '-o', 'json']
        if obj['metadata'].get('namespace'):
            words += ['-n', obj['metadata']['namespace']]
        return oc(*words)
    configuration = oc('get', 'configmap', 'browser-ci-installation', '-n', ci.APP_NS, '-o', 'json')['data']
    issuer = configuration['issuer']
    source = oc('get', 'clusterissuer', issuer, '-o', 'json')
    if not any(o.get('type') == 'Ready' and o.get('status') == 'True' for o in source.get('status', {}).get('conditions', [])):
        raise RuntimeError('The existing certificate issuer is not ready')
    storage = oc('get', 'storageclass', args.storage_class, '-o', 'json')
    if storage.get('reclaimPolicy') != 'Delete' or storage.get('provisioner') == 'kubernetes.io/no-provisioner':
        raise RuntimeError('Require dynamic storage with the Delete reclaim policy')
    endpoints = []
    for item in json.loads(configuration['kubernetes-endpoints.json'])['items']:
        for endpoint in item['endpoints']:
            if endpoint.get('conditions', {}).get('ready') is True:
                endpoints += [(address, port['port']) for address in endpoint['addresses'] for port in item['ports'] if port.get('name') == 'https' and port.get('protocol') == 'TCP']
    service = json.loads(configuration['kubernetes-service.json'])
    endpoints += [(address, 443) for address in service['spec'].get('clusterIPs', [service['spec']['clusterIP']])]
    objects, templates = ci.build(ci.upstream(), endpoints, issuer, args.storage_class)
    record.update({'issuer': issuer, 'storage_class': args.storage_class, 'storage_class_uid': storage['metadata']['uid']})
    lock = ci.module('cnpg_ci_installation_lock', 'jshell_live_lock.py')
    holder = 'cnpg-ci-installation'
    lock.acquire(args.context, holder, ci.APP_NS, 'service-check')
    def create(obj):
        obj['metadata'].setdefault('annotations', {})['stego.test/cnpg-installation-id'] = record['installation_id']
        entry = {'kind': obj['kind'], 'name': obj['metadata']['name'], 'namespace': obj['metadata'].get('namespace', ''), 'state': 'requested'}
        record['resources'].append(entry); save()
        try:
            value = oc('create', '-f', '-', '-o', 'json', data=obj)
        except (RuntimeError, subprocess.TimeoutExpired):
            value = get(obj)
            if value and value['metadata'].get('annotations', {}).get('stego.test/cnpg-installation-id') == record['installation_id']:
                entry.update({'uid': value['metadata']['uid'], 'state': 'observed_after_error'}); save()
            raise
        entry.update({'uid': value['metadata']['uid'], 'state': 'created'}); save()
        return value
    try:
        for obj in objects + [ci.resource('ConfigMap', ci.CONFIG, ci.APP_NS)]:
            if get(obj):
                raise RuntimeError('A CNPG installation target already exists; inspect its owner')
        # No CI write authority exists until policy and webhook trust are ready.
        grants = [o for o in objects if o['kind'] in {'RoleBinding', 'ClusterRoleBinding'} and ci.service_account('hypershell-ci', 'stego-ci-access') in o.get('subjects', [])]
        initial = [o for o in objects if o not in grants]
        initial.sort(key=lambda o: {'Namespace': 0, 'CustomResourceDefinition': 1}.get(o['kind'], 2))
        definitions = [o for o in initial if o['kind'] in {'Namespace', 'CustomResourceDefinition'}]
        for obj in definitions:
            create(obj)
        deadline = time.monotonic() + 120
        while not all(any(c.get('type') == 'Established' and c.get('status') == 'True'
                          for c in get(o).get('status', {}).get('conditions', []))
                      for o in definitions if o['kind'] == 'CustomResourceDefinition'):
            if time.monotonic() >= deadline:
                raise RuntimeError('CNPG type definitions did not become ready')
            time.sleep(2)
        for obj in initial:
            if obj not in definitions:
                create(obj)
        deadline = time.monotonic() + 180
        while True:
            ready = True
            for obj in initial:
                kind = obj['kind']
                if kind not in {'Certificate', 'ValidatingAdmissionPolicy', 'ValidatingWebhookConfiguration', 'MutatingWebhookConfiguration'}:
                    continue
                current = get(obj)
                status = current.get('status', {})
                if kind == 'Certificate':
                    ready = ready and any(o.get('type') == 'Ready' and o.get('status') == 'True' and o.get('observedGeneration') == current['metadata']['generation'] for o in status.get('conditions', []))
                elif kind == 'ValidatingAdmissionPolicy':
                    if status.get('typeChecking', {}).get('expressionWarnings'):
                        raise RuntimeError('CNPG CI admission policy has type errors')
                    ready = ready and status.get('observedGeneration') == current['metadata']['generation'] and 'typeChecking' in status
                else:
                    ready = ready and all(h.get('clientConfig', {}).get('caBundle') for h in current['webhooks'])
            if ready:
                break
            if time.monotonic() > deadline:
                raise RuntimeError('CNPG CI policy and trust did not become ready')
            time.sleep(2)
        for obj in grants:
            create(obj)
        namespaces = {ns: oc('get', 'namespace', ns, '-o', 'json')['metadata']['uid'] for ns in [ci.OPERATOR_NS, ci.DATABASE_NS]}
        data = {'record.json': json.dumps({'resources': record['resources'], 'namespaces': namespaces, 'issuer': issuer,
                 'storage_class': args.storage_class, 'storage_class_uid': storage['metadata']['uid'], 'installation_id': record['installation_id']})}
        data.update({name + '.json': json.dumps(value) for name, value in templates.items()})
        create(ci.resource('ConfigMap', ci.CONFIG, ci.APP_NS, immutable=True, data=data))
        record['complete'] = True; save()
        print('Installed fixed CNPG CI namespaces, policy, and webhook trust. No test Pod was started.')
    finally:
        # This command starts no test Job or Pod. Keep its journal for any
        # incomplete static installation, and never replace an existing owner.
        lock.release(args.context, holder)


if __name__ == '__main__':
    main()
