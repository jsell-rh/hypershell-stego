"""Mount the operator's internal Gateway CA in the browser test Job."""
import os
from pathlib import Path
import ssl

from public_gateway_fixture import CERTIFICATE


def apply_internal_fixture(document, workload, browser):
    path = os.environ.get('STEGO_TEST_GATEWAY_INTERNAL_CA_FILE', '')
    if not path:
        return
    if workload != '1' or browser != '1' or not Path(path).is_absolute():
        raise ValueError('Internal Gateway trust requires a complete workflow and an absolute path')
    with Path(path).open('rb') as source:
        raw = source.read((512 << 10) + 1)
    if len(raw) > 512 << 10:
        raise ValueError('Internal Gateway trust exceeds 512 KiB')
    bundle = raw.decode('ascii')
    certificates = CERTIFICATE.findall(bundle)
    if not 1 <= len(certificates) <= 256 or CERTIFICATE.sub('', bundle).strip():
        raise ValueError('Internal Gateway trust requires only certificates')
    ssl.create_default_context(cadata=bundle)
    maps = [item for item in document['items'] if item['kind'] == 'ConfigMap' and item['metadata']['name'] == 'database-ca']
    jobs = [item for item in document['items'] if item['kind'] == 'Job']
    if len(maps) != 1 or len(jobs) != 1 or 'gateway-internal-ca.pem' in maps[0].get('data', {}):
        raise ValueError('Internal Gateway trust fixture differs')
    maps[0].setdefault('data', {})['gateway-internal-ca.pem'] = bundle
    spec = jobs[0]['spec']['template']['spec']
    spec['volumes'].append({'name': 'gateway-internal-trust', 'configMap': {'name': 'database-ca', 'defaultMode': 0o440,
                           'items': [{'key': 'gateway-internal-ca.pem', 'path': 'ca.pem'}]}})
    test = spec['containers'][0]
    test['volumeMounts'].append({'name': 'gateway-internal-trust', 'mountPath': '/gateway-internal-trust', 'readOnly': True})
    test['env'].append({'name': 'STEGO_TEST_GATEWAY_INTERNAL_CA_FILE', 'value': '/gateway-internal-trust/ca.pem'})
