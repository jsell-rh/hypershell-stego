#!/usr/bin/env python3
"""Check the actual count runner render commands without cluster access."""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--render', type=Path, required=True)
    parser.add_argument('--results', type=Path, required=True)
    args = parser.parse_args()
    if not args.render.is_absolute() or not args.render.is_file() or not args.results.is_absolute():
        raise SystemExit('Select an absolute renderer and result path')
    args.results.mkdir(mode=0o700)
    path = Path(__file__).with_name('check-count-namespaces.py')
    spec = importlib.util.spec_from_file_location('count_runner', path)
    runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runner)
    namespace = 'stego-count-render-check'
    image = 'registry.example.test/count@sha256:' + 'a' * 64
    environment = dict(os.environ, STEGO_COUNT_RENDER=str(args.render),
                       STEGO_TEST_NAMESPACE=namespace, STEGO_TEST_IDLE_IMAGE=image,
                       KUBERNETES_SERVICE_HOST='192.0.2.1', KUBERNETES_SERVICE_PORT='443')
    # The fixture must supply the endpoint. Do not weaken the generated check.
    denied = subprocess.run([str(args.render), '--namespace', namespace, '--image', image,
                             '--worker', 'gateway-workload', '--egress', 'kubernetes=192.0.2.1:443'],
                            capture_output=True, timeout=10)
    if denied.returncode == 0 or denied.stdout or b'missing declared external endpoint' not in denied.stderr:
        raise SystemExit('The renderer did not reject the missing database endpoint before output')
    workers = ['namespace-allocation', 'gateway-workload', 'sandbox-count']
    records = {}
    for attempt in ['first', 'second']:
        directory = args.results / attempt
        directory.mkdir()
        environment['STEGO_COUNT_RENDER_RESULTS'] = str(directory)
        subprocess.run(['bash', '-e', '-u'], input=runner.RENDER_COUNT_WORKERS.encode(),
                       env=environment, check=True, timeout=30)
        if {p.name for p in directory.iterdir()} != {name + '.json' for name in workers}:
            raise SystemExit('The count runner did not render exactly its three workers')
        for worker in workers:
            data = (directory / (worker + '.json')).read_bytes()
            manifest = json.loads(data)
            if worker == 'namespace-allocation':
                bindings = runner.allocation_endpoints(manifest, namespace)
                if json.loads(bindings) != {'kubernetes': ['192.0.2.1:443']}:
                    raise SystemExit('The test allocator did not receive the rendered endpoint')
                (args.results / 'allocation-network-endpoints.json').write_bytes(bindings)
                for mode in ['absent', 'duplicate', 'indirect', 'wrong-namespace', 'unknown-endpoint', 'empty-addresses']:
                    invalid = copy.deepcopy(manifest)
                    deployment = next(item for item in invalid['items'] if item['kind'] == 'Deployment')
                    container = next(item for item in deployment['spec']['template']['spec']['containers']
                                     if any(env['name'] == 'STEGO_ALLOCATION_NETWORK_ENDPOINTS' for env in item.get('env', [])))
                    entry = next(env for env in container['env'] if env['name'] == 'STEGO_ALLOCATION_NETWORK_ENDPOINTS')
                    if mode == 'absent':
                        container['env'].remove(entry)
                    elif mode == 'duplicate':
                        container['env'].append(copy.deepcopy(entry))
                    elif mode == 'indirect':
                        entry['valueFrom'] = {'secretKeyRef': {'name': 'unused', 'key': 'unused'}}
                    elif mode == 'wrong-namespace':
                        deployment['metadata']['namespace'] = 'another-test'
                    elif mode == 'unknown-endpoint':
                        entry['value'] = '{"unexpected":["192.0.2.1:443"]}'
                    else:
                        entry['value'] = '{"kubernetes":[]}'
                    try:
                        runner.allocation_endpoints(invalid, namespace)
                    except RuntimeError:
                        pass
                    else:
                        raise SystemExit('The test allocator accepted invalid settings: ' + mode)
            if manifest.get('kind') != 'List' or not manifest.get('items'):
                raise SystemExit('The worker manifest is empty')
            names = {(item['kind'], item['metadata']['name']) for item in manifest['items']}
            if ('ServiceAccount', 'hypershell-' + worker) not in names:
                raise SystemExit('The worker manifest has no selected identity')
            if ('Deployment', 'hypershell-' + worker) not in names:
                raise SystemExit('The worker manifest has no selected Deployment')
            digest = hashlib.sha256(data).hexdigest()
            if attempt == 'first':
                records[worker] = digest
            elif records[worker] != digest:
                raise SystemExit('Repeated worker rendering changed its output')
    record = {'runner_sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
              'worker_manifest_sha256': records, 'workers_rendered_twice': len(workers),
              'missing_required_endpoint_rejected_before_output': True,
              'test_allocator_uses_rendered_endpoints': True,
              'cluster_access_used': False, 'scope': 'Count fixture render inputs only. No live count behavior is proved.'}
    (args.results / 'verification.json').write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(record, indent=2))


if __name__ == '__main__':
    main()
