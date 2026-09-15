#!/usr/bin/env python3
"""Run the complete CNPG Gateway workflow with only the restricted CI identity."""
import sys
sys.dont_write_bytecode = True
import argparse
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
from types import SimpleNamespace

import cnpg_ci as ci
from ci_credentials import CNPG_SECONDS, require_context_credentials


def resource_owned(value, holder, cluster_uid):
    metadata = value['metadata']
    if holder and metadata.get('annotations', {}).get('stego.test/cnpg-holder') == holder:
        return True
    return bool(cluster_uid) and any(o.get('uid') == cluster_uid and o.get('kind') == 'Cluster' and o.get('apiVersion') == 'postgresql.cnpg.io/v1' for o in metadata.get('ownerReferences', []))


def database_pods(pods):
    # The independent lifetime Pod must remain until SQL Pod cleanup finishes.
    return [pod for pod in pods if pod['metadata'].get('labels', {}).get('cnpg.io/cluster') == 'gateway-database']


def admission_probe(context, value, policy, expected):
    response = subprocess.run(['oc', '--context=' + context, '--request-timeout=20s', 'create',
                               '--dry-run=server', '-f', '-', '-o', 'json'],
                              input=json.dumps(value), text=True, capture_output=True, timeout=30)
    if expected:
        if response.returncode or json.loads(response.stdout).get('kind') != value['kind']:
            raise RuntimeError('CNPG admission did not accept the fixed runtime template')
    elif response.returncode == 0 or policy not in response.stderr or 'denied request' not in response.stderr:
        raise RuntimeError('CNPG admission denial was not confirmed by its policy')



def access_probe(context, verb, resource, namespace):
    words = ['oc', '--context=' + context, '--request-timeout=15s', 'auth', 'can-i', verb]
    if resource == 'pods/exec':
        words += ['pods', '--subresource=exec']
    else:
        words.append(resource)
    if namespace:
        words += ['-n', namespace]
    return subprocess.run(words, text=True, capture_output=True, timeout=25)



def verify_allocations(browser):
    # The fixed allocator identity can list namespaces. The CI identity cannot.
    # Reuse the browser runner's bounded client in its read-only mode.
    ca = browser / 'ci-ca.pem'
    result = browser / 'allocation-final.json'
    result.unlink(missing_ok=True)
    subprocess.run([str(browser / 'allocation-cleanup'), '--namespace', ci.APP_NS,
                    '--server', (browser / 'ci-server').read_text().strip(),
                    '--ca-file', str(ca) if ca.stat().st_size else '',
                    '--token-file', str(browser / 'ci-token'), '--result', str(result)],
                   check=True, timeout=195)
    evidence = json.loads(result.read_text())
    if evidence.get('allocations_absent') is not True or evidence.get('allocations_before') != 0:
        raise RuntimeError('Gateway allocation absence is not confirmed; retain the SQL server and Lease')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--repository', type=Path, required=True)
    parser.add_argument('--kubeconfig', type=Path, required=True)
    parser.add_argument('--results', type=Path, required=True)
    args = parser.parse_args()
    source = args.source.resolve()
    context = 'jshell-ci'
    if args.kubeconfig.is_symlink() or not args.kubeconfig.is_file() or args.kubeconfig.stat().st_mode & 0o077:
        raise RuntimeError('Require a private CI kubeconfig')
    os.environ['KUBECONFIG'] = str(args.kubeconfig.resolve())
    require_context_credentials(context, CNPG_SECONDS, args.kubeconfig.resolve())
    workflow = ci.module('cnpg_ci_workflow', 'check-cnpg-installation.py')
    frozen = workflow.verify_source(source, args.repository.resolve())
    if frozen['cnpg_installation']['namespace'] != ci.DATABASE_NS:
        raise RuntimeError('The source must select the fixed CNPG CI namespace')
    args.results.mkdir(mode=0o700, parents=False, exist_ok=False)
    os.umask(0o077)
    fixture = ci.module('cnpg_ci_fixture_runner', 'cnpg-installation-fixture.py')
    lock = ci.module('cnpg_ci_live_lock', 'jshell_live_lock.py')
    holder = 'stego-cnpg-live-' + secrets.token_hex(8)
    settings = SimpleNamespace(context=context, namespace=ci.APP_NS, database_namespace=ci.DATABASE_NS,
                               results=args.results, lease_holder=holder, lease_uid='')
    client = fixture.Installation(settings)
    config = client.get('configmap', ci.CONFIG, ci.APP_NS)
    if not config or config.get('immutable') is not True or config['metadata'].get('labels', {}).get('app.kubernetes.io/managed-by') != 'stego-cnpg-ci':
        raise RuntimeError('Require the immutable operator-installed CNPG CI record')
    record = json.loads(config['data']['record.json'])
    if set(record['namespaces']) != {ci.OPERATOR_NS, ci.DATABASE_NS}:
        raise RuntimeError('CNPG installation namespace set differs')
    for namespace, uid in record['namespaces'].items():
        current = client.get('namespace', namespace)
        if not current or current['metadata']['uid'] != uid or current['metadata'].get('labels', {}).get('app.kubernetes.io/managed-by') != 'stego-cnpg-ci':
            raise RuntimeError('CNPG installation namespace identity differs')
    settings.storage_class = record['storage_class']
    storage = client.get('storageclass', settings.storage_class)
    if not storage or storage['metadata']['uid'] != record['storage_class_uid'] or storage.get('reclaimPolicy') != 'Delete':
        raise RuntimeError('The installed storage class identity or reclaim policy differs')
    templates = {key: json.loads(config['data'][key + '.json']) for key in ['operator-job', 'database-job', 'cluster']}
    for key, kind, name, namespace in [
        ('operator-job', 'Job', 'cnpg-operator', ci.OPERATOR_NS),
        ('database-job', 'Job', 'database-lifetime', ci.DATABASE_NS),
        ('cluster', 'Cluster', 'gateway-database', ci.DATABASE_NS),
    ]:
        value = templates[key]
        if value['kind'] != kind or value['metadata']['name'] != name or value['metadata'].get('namespace') != namespace:
            raise RuntimeError('CNPG runtime template target differs')
    # These are SelfSubjectAccessReviews. No impersonation credential is used.
    probes = [
        (True, 'create', 'jobs', ci.OPERATOR_NS),
        (True, 'create', 'clusters.postgresql.cnpg.io', ci.DATABASE_NS),
        (False, 'create', 'namespaces', None),
        (False, 'create', 'clusters.postgresql.cnpg.io', 'default'),
        (False, 'patch', 'customresourcedefinitions.apiextensions.k8s.io', None),
        (False, 'patch', 'validatingwebhookconfigurations.admissionregistration.k8s.io', None),
        (False, 'create', 'rolebindings', ci.DATABASE_NS),
        (False, 'patch', 'networkpolicies', ci.DATABASE_NS),
        (False, 'get', 'secrets', ci.OPERATOR_NS),
        (False, 'create', 'pods/exec', ci.OPERATOR_NS),
        (False, 'delete', 'configmaps/' + ci.CONFIG, ci.APP_NS),
    ]
    access = []
    for expected, verb, resource, namespace in probes:
        response = access_probe(context, verb, resource, namespace)
        value = response.stdout.strip()
        if value not in {'yes', 'no'} or (value == 'yes') != expected or response.returncode != (0 if expected else 1):
            raise RuntimeError('CNPG CI access boundary differs: ' + verb + ' ' + resource)
        access.append({'verb': verb, 'resource': resource, 'namespace': namespace, 'allowed': expected})
    fixture.write(args.results / 'cnpg-ci-access.json', access)
    lock.acquire(context, holder, ci.APP_NS, 'service-check')
    settings.lease_uid = lock.lease(context)['metadata']['uid']
    state = {'source_base_commit': frozen['source_base_commit'], 'compiler_revision': frozen['compiler_revision'],
             'holder': holder, 'lease_uid': settings.lease_uid, 'installation_config_uid': config['metadata']['uid'], 'phase': 'lease_acquired'}
    save = lambda: fixture.write(args.results / 'cnpg-ci-run.json', state)
    save()
    journal = []
    result = 1
    cluster_uid = ''
    admission = []
    def probe(name, value, policy, expected=False):
        admission_probe(context, value, policy, expected)
        admission.append({'name': name, 'policy': policy, 'allowed': expected})
        fixture.write(args.results / 'cnpg-ci-admission.json', admission)
    def items(kind, namespace):
        return client.oc('get', kind, '-n', namespace, '-o', 'json')['items']
    def require_empty():
        for namespace, kinds in [(ci.OPERATOR_NS, ['jobs', 'pods']), (ci.DATABASE_NS, ['clusters.postgresql.cnpg.io', 'jobs', 'pods', 'persistentvolumeclaims'])]:
            if any(items(kind, namespace) for kind in kinds):
                raise RuntimeError('An earlier CNPG runtime remains; retain the Lease for inspection')
        if volumes():
            raise RuntimeError('An earlier CNPG volume remains; retain the Lease for inspection')
    def volumes():
        return [v for v in client.oc('get', 'persistentvolumes', '-o', 'json')['items'] if v.get('spec', {}).get('claimRef', {}).get('namespace') == ci.DATABASE_NS]
    def create(value):
        value = copy.deepcopy(value)
        value['metadata'].setdefault('labels', {})[fixture.LABEL] = ci.APP_NS
        return client.create(value, journal)
    def wait(check, seconds, message):
        deadline = time.monotonic() + seconds
        while not check():
            if time.monotonic() >= deadline:
                raise RuntimeError(message)
            time.sleep(2)
    def owned(value):
        return resource_owned(value, holder, cluster_uid)
    def delete(value, plural):
        if not owned(value):
            raise RuntimeError('CNPG cleanup refuses an unowned object')
        metadata = value['metadata']
        prefix = '/api/v1' if value['apiVersion'] == 'v1' else '/apis/' + value['apiVersion']
        target = prefix + '/namespaces/' + metadata['namespace'] + '/' + plural + '/' + metadata['name']
        client.oc('delete', '--raw=' + target, '-f', '-', data={'apiVersion': 'v1', 'kind': 'DeleteOptions',
                  'preconditions': {'uid': metadata['uid'], 'resourceVersion': metadata['resourceVersion']}, 'propagationPolicy': 'Foreground'})
    def delete_record(entry, plural):
        metadata = entry['metadata']
        current = client.get(plural, metadata['name'], metadata['namespace'])
        if current:
            if current['metadata']['uid'] != metadata.get('uid'):
                raise RuntimeError('CNPG runtime object identity changed')
            delete(current, plural)
    def app_absent():
        for kind in ['jobs', 'pods', 'deployments']:
            if items(kind, ci.APP_NS):
                raise RuntimeError('Application runtime remains; retain its SQL server and Lease')
        for kind in ['secrets', 'services', 'networkpolicies', 'configmaps']:
            if client.oc('get', kind, '-n', ci.APP_NS, '-l', 'stego.test/browser-run=' + ci.APP_NS, '-o', 'json')['items']:
                raise RuntimeError('Application test data remains; retain its SQL server and Lease')
        marker = hashlib.sha256((ci.APP_NS + '.hypershell-namespace-allocation').encode()).hexdigest()[:32]
        for kind in ['clusterroles', 'clusterrolebindings']:
            if client.oc('get', kind, '-l', 'stego.dev/allocator=' + marker, '-o', 'json')['items']:
                raise RuntimeError('Gateway allocations remain; retain the SQL server and Lease')
        verify_allocations(args.results / 'browser')
    try:
        require_empty()
        if client.get('secret', 'cnpg-credentials', ci.APP_NS):
            raise RuntimeError('An earlier private SQL fixture remains')
        fixture.write(client.journal, journal)
        for key, namespace in [('operator-job', ci.OPERATOR_NS), ('database-job', ci.DATABASE_NS)]:
            policy = namespace + '.bounded-jobs'
            probe(key + '-accepted', templates[key], policy, True)
            for name, change in [
                ('missing-deadline', lambda v: v['spec'].pop('activeDeadlineSeconds')),
                ('suspended', lambda v: v['spec'].update(suspend=True)),
                ('delayed-cleanup', lambda v: v['spec'].update(ttlSecondsAfterFinished=3600)),
            ]:
                bad = copy.deepcopy(templates[key]); change(bad)
                probe(key + '-' + name, bad, policy)
        policy = ci.DATABASE_NS + '.bounded-jobs'
        for name, change in [
            ('command', lambda v: v['spec']['template']['spec']['containers'][0].update(command=['/bin/sh'])),
            ('environment', lambda v: v['spec']['template']['spec']['containers'][0].update(env=[{'name': 'LD_PRELOAD', 'value': '/tmp/library.so'}])),
            ('probe-command', lambda v: v['spec']['template']['spec']['containers'][0].update(livenessProbe={'exec': {'command': ['/bin/true']}})),
            ('network-label', lambda v: v['spec']['template'].setdefault('metadata', {}).setdefault('labels', {}).update({'cnpg.io/cluster': 'gateway-database'})),
        ]:
            bad = copy.deepcopy(templates['database-job']); change(bad)
            probe('database-lifetime-' + name, bad, policy)
        policy = ci.OPERATOR_NS + '.bounded-jobs'
        for name, change in [
            ('command', lambda c: c.update(command=['/bin/sh'])),
            ('scope', lambda c: c['env'][0].update(value='default')),
            ('environment-source', lambda c: c.update(envFrom=[{'configMapRef': {'name': 'injected'}}])),
            ('lifecycle', lambda c: c.update(lifecycle={'postStart': {'exec': {'command': ['/bin/true']}}})),
            ('probe-command', lambda c: c.update(livenessProbe={'exec': {'command': ['/bin/true']}})),
            ('executable-mount', lambda c: c['volumeMounts'][0].update(mountPath='/manager')),
        ]:
            bad = copy.deepcopy(templates['operator-job']); change(bad['spec']['template']['spec']['containers'][0])
            probe('operator-' + name, bad, policy)
        operator_job = create(templates['operator-job'])
        def operator_ready():
            pods = items('pods', ci.OPERATOR_NS)
            return any(any(o.get('uid') == operator_job['metadata']['uid'] for o in p['metadata'].get('ownerReferences', [])) and
                       any(c.get('type') == 'Ready' and c.get('status') == 'True' for c in p.get('status', {}).get('conditions', [])) for p in pods)
        wait(operator_ready, 300, 'CNPG operator readiness was not confirmed')
        state['phase'] = 'operator_ready'; save()
        database_job = create(templates['database-job'])
        owner = {'apiVersion': 'batch/v1', 'kind': 'Job', 'name': database_job['metadata']['name'], 'uid': database_job['metadata']['uid']}
        password = secrets.token_hex(32)
        create({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'fixture-postgres', 'namespace': ci.DATABASE_NS, 'ownerReferences': [owner]},
                'type': 'kubernetes.io/basic-auth', 'stringData': {'username': 'postgres', 'password': password}})
        cluster = copy.deepcopy(templates['cluster'])
        cluster['metadata']['ownerReferences'] = [owner]
        cluster['spec'].setdefault('inheritedMetadata', {}).setdefault('annotations', {})['stego.test/cnpg-holder'] = holder
        policy = ci.DATABASE_NS + '.bounded-cluster'
        probe('cluster-accepted', cluster, policy, True)
        for name, change in [
            ('instance-count', lambda v: v['spec'].update(instances=3)),
            ('storage-size', lambda v: v['spec']['storage'].update(size='2Gi')),
            ('missing-owner', lambda v: v['metadata'].pop('ownerReferences')),
        ]:
            bad = copy.deepcopy(cluster); change(bad)
            probe('cluster-' + name, bad, policy)
        current = create(cluster)
        cluster_uid = current['metadata']['uid']
        state['cluster_uid'] = cluster_uid; save()
        def database_ready():
            current = client.get('clusters.postgresql.cnpg.io', 'gateway-database', ci.DATABASE_NS)
            return current and current['metadata']['uid'] == cluster_uid and current.get('status', {}).get('readyInstances') == 2
        wait(database_ready, 360, 'CNPG database readiness was not confirmed')
        current = client.get('clusters.postgresql.cnpg.io', 'gateway-database', ci.DATABASE_NS)
        ca_name = current['status']['certificates']['serverCASecret']
        if not isinstance(ca_name, str) or not ca_name.startswith('gateway-database-'):
            raise RuntimeError('CNPG CA reference differs')
        ca = base64.b64decode(client.get('secret', ca_name, ci.DATABASE_NS)['data']['ca.crt'], validate=True).decode()
        ready = {'namespace': ci.DATABASE_NS, 'namespace_uid': record['namespaces'][ci.DATABASE_NS], 'cluster_uid': cluster_uid, 'ready_instances': 2}
        fixture.write(args.results / 'cnpg-server-ready.json', ready)
        private = dict(ready, cluster='gateway-database', password=password, ca=ca)
        private.pop('ready_instances')
        create({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'cnpg-credentials', 'namespace': ci.APP_NS},
                'type': 'Opaque', 'stringData': {'fixture.json': json.dumps(private)}})
        state['phase'] = 'application_running'; save()
        environment = dict(os.environ, PYTHONDONTWRITEBYTECODE='1', STEGO_TEST_CONTEXT=context,
            STEGO_TEST_PREINSTALLED='1', STEGO_TEST_BROWSER_DEPLOYMENT='1', STEGO_TEST_BROWSER_WORKLOAD='1',
            STEGO_TEST_GATEWAY_CLUSTER_ISSUER=record['issuer'], STEGO_TEST_RESULTS=str((args.results / 'browser').resolve()),
            STEGO_TEST_CNPG_FIXTURE='1', STEGO_TEST_HELD_LEASE_HOLDER=holder, STEGO_TEST_HELD_LEASE_UID=settings.lease_uid)
        with (args.results / 'browser-run.log').open('w') as log:
            result = subprocess.run(['bash', str(source / 'scripts/check-service-deployment.sh')], cwd=source, env=environment,
                                    stdout=log, stderr=subprocess.STDOUT).returncode
        if result:
            raise RuntimeError('CNPG browser workflow failed; inspect its saved Job and logs')
        state['restart'] = workflow.verify_application(args.results / 'browser', ready)
        state['phase'] = 'application_passed'; save()
    finally:
        app_absent()
        # Record volume identities before deleting their claims. The CI identity
        # can read PV metadata, but only the storage controller can remove a PV.
        claims = items('persistentvolumeclaims', ci.DATABASE_NS)
        for claim in claims:
            if not owned(claim):
                raise RuntimeError('A database claim has an unknown owner; retain the Lease')
        state['volumes'] = [{'name': v['metadata']['name'], 'uid': v['metadata']['uid']} for v in volumes()]; save()
        for entry in reversed(journal):
            if entry['kind'] == 'Cluster':
                delete_record(entry, 'clusters.postgresql.cnpg.io')
        wait(lambda: not items('clusters.postgresql.cnpg.io', ci.DATABASE_NS) and not database_pods(items('pods', ci.DATABASE_NS)), 180, 'CNPG Cluster cleanup is incomplete')
        for claim in claims:
            current = client.get('persistentvolumeclaims', claim['metadata']['name'], ci.DATABASE_NS)
            if current:
                if current['metadata']['uid'] != claim['metadata']['uid']:
                    raise RuntimeError('Database claim identity changed')
                delete(current, 'persistentvolumeclaims')
        wait(lambda: not items('persistentvolumeclaims', ci.DATABASE_NS) and not volumes(), 180, 'CNPG volume cleanup is incomplete')
        for entry in reversed(journal):
            if entry['kind'] == 'Secret':
                delete_record(entry, 'secrets')
            elif entry['kind'] == 'Job' and entry['metadata']['namespace'] == ci.DATABASE_NS:
                delete_record(entry, 'jobs')
        wait(lambda: not items('jobs', ci.DATABASE_NS) and not items('pods', ci.DATABASE_NS), 180, 'Database lifetime cleanup is incomplete')
        for entry in reversed(journal):
            if entry['kind'] == 'Job' and entry['metadata']['namespace'] == ci.OPERATOR_NS:
                delete_record(entry, 'jobs')
        wait(lambda: not items('jobs', ci.OPERATOR_NS) and not items('pods', ci.OPERATOR_NS), 180, 'Operator cleanup is incomplete')
        require_empty()
        for kind in ['secrets', 'services', 'configmaps', 'roles', 'rolebindings']:
            if client.oc('get', kind, '-n', ci.DATABASE_NS, '-l', 'cnpg.io/cluster=gateway-database', '-o', 'json')['items']:
                raise RuntimeError('CNPG child resources remain; retain the Lease')
        if client.get('secret', 'cnpg-credentials', ci.APP_NS):
            raise RuntimeError('The private CNPG fixture remains')
        fixture.write(args.results / 'cnpg-ci-cleanup.json', {'runtime_absent': True, 'volumes_absent': True, 'private_fixture_absent': True, 'installation_retained': True})
        lock.release(context, holder)
        state['phase'] = 'complete' if result == 0 and 'restart' in state else 'failed_with_cleanup'; save()
    print('CNPG Gateway CI passed with complete runtime and volume cleanup.')


if __name__ == '__main__':
    main()
