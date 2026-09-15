#!/usr/bin/env python3
"""Run the complete Gateway browser workflow with an installation CNPG server.

The operator installs and removes the test server. The browser runner uses only
its restricted CI kubeconfig. Keep the shared Lease until all cleanup passes.
"""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tarfile
import time

sys.dont_write_bytecode = True

from ci_credentials import CNPG_SECONDS, require_context_credentials


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def verify_source(source, repository):
    record = json.loads((source / 'acceptance/browser-inspection-source.json').read_text())
    if not re.fullmatch(r'[0-9a-f]{40}', record['source_base_commit']) or not re.fullmatch(r'[0-9a-f]{40}', record['compiler_revision']):
        raise RuntimeError('The frozen source has no complete revision identity')
    if record.get('cnpg_installation', {}).get('cluster') != 'gateway-database':
        raise RuntimeError('Prepare the frozen CNPG inspection source first')
    archive = subprocess.check_output(['git', '-C', str(repository), 'archive', record['source_base_commit']], timeout=30)
    with tarfile.open(fileobj=io.BytesIO(archive)) as files:
        payloads = {item.name: files.extractfile(item).read() for item in files if item.isfile()}
        committed = {name: hashlib.sha256(value).hexdigest() for name, value in payloads.items()}
    if committed != record['source_sha256'] or set(committed) != set(record['fixture_sha256']):
        raise RuntimeError('The frozen source does not match its repository commit')
    changed = {name for name in committed if committed[name] != record['fixture_sha256'][name]}
    allowed = {'service.yaml', '.stego/state.yaml', 'out/deploy/allocation/allocation.go',
               'out/deploy/render/worker-namespace-allocation.json.tmpl', 'out/deploy/render/worker-gateway-workload.json.tmpl'}
    if changed != allowed or set(record['changed_files']) != allowed:
        raise RuntimeError('The frozen CNPG source has unexpected changes')
    for name, expected in record['fixture_sha256'].items():
        relative = Path(name)
        path = source / relative
        if relative.is_absolute() or '..' in relative.parts or path.is_symlink() or not path.is_file():
            raise RuntimeError('Invalid frozen source path')
        with path.open('rb') as file:
            actual = hashlib.file_digest(file, 'sha256').hexdigest()
        if actual != expected:
            raise RuntimeError('The frozen source changed: ' + name)
    if (source / '.stego/compiler-revision').read_text().strip() != record['compiler_revision']:
        raise RuntimeError('The frozen compiler pin differs')
    inspection = module('cnpg_inspection_verify', source / 'scripts/prepare-browser-inspection.py')
    expected_declaration = inspection.cnpg_declaration(inspection.declaration(payloads['service.yaml'].decode()), record['cnpg_installation']['namespace'])
    if (source / 'service.yaml').read_text() != expected_declaration:
        raise RuntimeError('The CNPG fixture declaration exceeds its allowed additions')
    runtime = 'out/deploy/allocation/allocation.go'
    inspection.verify_runtime(payloads[runtime].decode(), (source / runtime).read_text(),
                              record['cnpg_installation']['namespace'])
    observed = {str(p.relative_to(source)) for p in source.rglob('*') if p.is_file()}
    if observed - {'acceptance/browser-inspection-source.json', '.stego/apply.lock'} != set(record['fixture_sha256']):
        raise RuntimeError('The frozen source inventory changed')
    return record


def verify_application(directory, installation):
    job = json.loads((directory / 'job-status.json').read_text())
    if not any(c['type'] == 'Complete' and c['status'] == 'True' for c in job.get('status', {}).get('conditions', [])):
        raise RuntimeError('The browser Job did not complete')
    log = (directory / 'deployment.log').read_text()
    if not re.search(r'^--- PASS: TestGeneratedKubernetesBrowserGatewayWorkflow ', log, re.M) or re.search(r'^--- FAIL:', log, re.M):
        raise RuntimeError('The complete browser workflow did not pass')
    cleanup = json.loads((directory / 'cleanup.json').read_text())
    if cleanup != {'namespace_retained': 'stego-service-ci', 'test_resources_absent': True, 'allocations_absent': True}:
        raise RuntimeError('Browser resource cleanup is not confirmed')
    with tarfile.open(directory / 'evidence.tar') as archive:
        if archive.extractfile('deployment.exit').read().strip() != b'0':
            raise RuntimeError('The browser application did not pass')
        generation = [archive.extractfile(name).read() for name in ['first.sha256', 'second.sha256', 'after-tests.sha256']]
        if len(set(generation)) != 1:
            raise RuntimeError('CNPG workflow generation records differ')
        restart = json.loads(archive.extractfile('browser-artifacts/cnpg-restart.json').read())
    for key in ['namespace', 'namespace_uid', 'cluster_uid']:
        if restart[key] != installation[key]:
            raise RuntimeError('CNPG restart evidence has a different installation identity')
    for key in ['cluster_spec_unchanged', 'sql_object_ids_unchanged', 'gateway_credentials_and_keys_unchanged', 'provider_data_unchanged', 'installation_data_unchanged']:
        if restart[key] is not True:
            raise RuntimeError('CNPG restart did not preserve required state')
    if not restart['old_pod_uid'] or not restart['new_primary_pod_uid'] or restart['old_pod_uid'] == restart['new_primary_pod_uid'] or restart['ready_instances'] != 2:
        raise RuntimeError('CNPG Pod replacement is not confirmed')
    if not 0 < restart['seconds'] < 300 or restart['primary_changed'] != (restart['old_primary'] != restart['new_primary']):
        raise RuntimeError('The CNPG restart record is inconsistent')
    return restart


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--operator-context', required=True)
    parser.add_argument('--ci-kubeconfig', type=Path, required=True)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--repository', type=Path, required=True, help='Local repository for exact commit verification')
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--storage-class', default='gp3-csi')
    args = parser.parse_args()
    source = args.source.resolve()
    record = verify_source(source, args.repository.resolve())
    namespace = 'stego-service-ci'
    database_namespace = record['cnpg_installation']['namespace']
    if args.ci_kubeconfig.is_symlink() or not args.ci_kubeconfig.is_file() or args.ci_kubeconfig.stat().st_mode & 0o077:
        raise RuntimeError('Require a private CI kubeconfig file')
    require_context_credentials('jshell-ci', CNPG_SECONDS, args.ci_kubeconfig.resolve())
    args.results.mkdir(mode=0o700, parents=False, exist_ok=False)
    os.umask(0o077)
    operator_results = args.results / 'operator'
    operator_results.mkdir(mode=0o700)
    lock = module('cnpg_live_lock', source / 'scripts/jshell_live_lock.py')
    server = module('cnpg_server', source / 'scripts/cnpg-installation-fixture.py')
    server.validate(namespace, database_namespace, args.storage_class)
    operator = module('cnpg_operator', source / 'scripts/cnpg-test-operator.py')
    holder = 'stego-cnpg-live-' + secrets.token_hex(8)
    lock.acquire(args.operator_context, holder, namespace, 'service-check')
    uid = lock.lease(args.operator_context)['metadata']['uid']
    state = {'holder': holder, 'lease_uid': uid, 'source_base_commit': record['source_base_commit'], 'compiler_revision': record['compiler_revision'], 'phase': 'lease_acquired'}
    save = lambda: server.write(args.results / 'cnpg-run.json', state)
    save()
    server_args = argparse.Namespace(context=args.operator_context, namespace=namespace, database_namespace=database_namespace,
                                     storage_class=args.storage_class, results=args.results, lease_holder=holder, lease_uid=uid)
    installation = server.Installation(server_args)
    operator_args = argparse.Namespace(context=args.operator_context, namespace=namespace, database_namespace=database_namespace, evidence=operator_results)
    result = 1
    try:
        if installation.get('namespace', database_namespace) or installation.get('secret', 'cnpg-credentials', namespace):
            raise RuntimeError('Inspect the existing CNPG installation before another run')
        if installation.get('job', 'service-check', namespace):
            raise RuntimeError('The old browser Job still exists')
        operator.prepare(operator_args)
        operator.install(operator_args)
        state['phase'] = 'operator_installed'; save()
        installation.install()
        state['phase'] = 'server_ready'; save()
        config = installation.get('configmap', 'browser-ci-installation', namespace)
        environment = dict(os.environ, PYTHONDONTWRITEBYTECODE='1', KUBECONFIG=str(args.ci_kubeconfig.resolve()), STEGO_TEST_CONTEXT='jshell-ci',
                           STEGO_TEST_PREINSTALLED='1', STEGO_TEST_BROWSER_DEPLOYMENT='1', STEGO_TEST_BROWSER_WORKLOAD='1',
                           STEGO_TEST_GATEWAY_CLUSTER_ISSUER=config['data']['issuer'], STEGO_TEST_RESULTS=str((args.results / 'browser').resolve()),
                           STEGO_TEST_CNPG_FIXTURE='1', STEGO_TEST_HELD_LEASE_HOLDER=holder, STEGO_TEST_HELD_LEASE_UID=uid)
        state['phase'] = 'application_running'; save()
        # The frozen script and cluster Jobs contain their own time limits. A
        # failed or lost host observation must not start a replacement Job.
        with (args.results / 'browser-run.log').open('w') as log:
            result = subprocess.run(['bash', str(source / 'scripts/check-service-deployment.sh')], cwd=source, env=environment,
                                    stdout=log, stderr=subprocess.STDOUT).returncode
        if result:
            raise RuntimeError('CNPG browser workflow failed; inspect its recorded Job and logs')
        ready = json.loads((args.results / 'cnpg-server-ready.json').read_text())
        state['restart'] = verify_application(args.results / 'browser', ready)
        state['phase'] = 'application_passed'; save()
    finally:
        # Read absence after the inner runner returns. Do not tear down the
        # server if an application process or Gateway allocation remains.
        for kind in ['jobs', 'pods', 'deployments']:
            active = installation.oc('get', kind, '-n', namespace, '-o', 'json')
            if active['items']:
                raise RuntimeError('Application resources remain; keep the CNPG server and Lease')
        for kind in ['secrets', 'services', 'networkpolicies', 'configmaps']:
            remaining = installation.oc('get', kind, '-n', namespace, '-l', 'stego.test/browser-run=' + namespace, '-o', 'json')
            if remaining['items']:
                raise RuntimeError('Application test data remains; keep the CNPG server and Lease')
        marker = hashlib.sha256((namespace + '.hypershell-namespace-allocation').encode()).hexdigest()[:32]
        for kind in ['namespaces', 'clusterroles', 'clusterrolebindings']:
            if installation.oc('get', kind, '-l', 'stego.dev/allocator=' + marker, '-o', 'json')['items']:
                raise RuntimeError('Gateway allocation resources remain; keep the CNPG server and Lease')
        installation.remove()
        if (operator_results / 'cnpg-created.json').exists():
            operator.remove(operator_args)
            created = json.loads((operator_results / 'cnpg-created.json').read_text())
            deadline = time.monotonic() + 180
            while any(operator.get(args.operator_context, item) for item in created):
                if time.monotonic() > deadline:
                    raise RuntimeError('CNPG operator cleanup is not complete; retain the Lease')
                time.sleep(2)
        lock.release(args.operator_context, holder)
        state['phase'] = 'complete' if result == 0 and 'restart' in state else 'failed_with_cleanup'
        save()
    print('CNPG Gateway workflow passed with complete cleanup. Results: ' + str(args.results))


if __name__ == '__main__':
    main()
