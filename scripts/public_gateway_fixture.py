"""Read explicit public Gateway test inputs and mount them in the test Job."""
import ipaddress
import hashlib
import json
import os
from pathlib import Path
import re
import ssl

LABEL = re.compile(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?')
CERTIFICATE = re.compile(r'-----BEGIN CERTIFICATE-----\s+[A-Za-z0-9+/=\s]+-----END CERTIFICATE-----')


def distinct_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('Public Gateway input contains a repeated field')
        result[key] = value
    return result


def read_config(path):
    with Path(path).open('rb') as source:
        raw = source.read((64 << 10) + 1)
    if len(raw) > 64 << 10:
        raise ValueError('Public Gateway input exceeds 64 KiB')
    config = json.loads(raw, object_pairs_hook=distinct_object)
    if not isinstance(config, dict) or set(config) != {'domain', 'issuer', 'router', 'ca_pem', 'endpoints'}:
        raise ValueError('Public Gateway input fields differ')
    for name in ['domain', 'issuer', 'router', 'ca_pem']:
        if not isinstance(config[name], str) or not config[name]:
            raise ValueError('Public Gateway text input is missing')
    domain = config['domain']
    if len(domain) > 223 or '.' not in domain or not all(LABEL.fullmatch(label) for label in domain.split('.')):
        raise ValueError('Public Gateway domain is invalid')
    try:
        ipaddress.ip_address(domain)
    except ValueError:
        pass
    else:
        raise ValueError('Public Gateway domain must be a DNS name')
    if not LABEL.fullmatch(config['issuer']) or not LABEL.fullmatch(config['router']):
        raise ValueError('Public Gateway issuer or router is invalid')
    bundle = config['ca_pem']
    if not 1 <= len(CERTIFICATE.findall(bundle)) <= 16 or CERTIFICATE.sub('', bundle).strip():
        raise ValueError('Public Gateway trust requires one through 16 certificates')
    ssl.create_default_context(cadata=bundle)
    endpoints = config['endpoints']
    if not isinstance(endpoints, list) or not 1 <= len(endpoints) <= 16:
        raise ValueError('Public Gateway requires 1 to 16 router addresses')
    canonical = []
    for endpoint in endpoints:
        if not isinstance(endpoint, str):
            raise ValueError('Public Gateway router address must be text')
        match = re.fullmatch(r'(?:\[([^\]]+)\]|([^:]+)):443', endpoint)
        if not match:
            raise ValueError('Public Gateway router address requires TCP port 443')
        ip = ipaddress.ip_address(match[1] or match[2])
        if (match[1] is not None and ip.version != 6) or str(ip) == '255.255.255.255':
            raise ValueError('Public Gateway router address is invalid')
        if ip.is_unspecified or ip.is_loopback or ip.is_link_local or ip.is_multicast or getattr(ip, 'ipv4_mapped', None) or '%' in str(ip):
            raise ValueError('Public Gateway router address must be unicast')
        canonical.append(f'[{ip}]:443' if ip.version == 6 else f'{ip}:443')
    if len(set(canonical)) != len(canonical):
        raise ValueError('Public Gateway router address is repeated')
    config['endpoints'] = sorted(canonical)
    return config


def apply_public_fixture(document, namespace, workload, browser):
    path = os.environ.get('STEGO_TEST_GATEWAY_PUBLIC_CONFIG', '')
    required = os.environ.get('STEGO_TEST_REQUIRE_PUBLIC_GATEWAY', '0')
    if required not in ('0', '1') or (required == '1' and not path):
        raise ValueError('The public Gateway test requires explicit configuration')
    if not path:
        return
    if workload != '1' or browser != '1':
        raise ValueError('Public Gateway requires the complete browser workflow')
    config = read_config(path)
    jobs = [item for item in document['items'] if item['kind'] == 'Job']
    if len(jobs) != 1:
        raise ValueError('Public Gateway requires one test Job')
    name = 'gateway-public-config'
    maps = [item for item in document['items'] if item['kind'] == 'ConfigMap' and item['metadata']['name'] == 'database-ca']
    if len(maps) != 1 or 'gateway-public.json' in maps[0].get('data', {}):
        raise ValueError('Public Gateway fixture trust map differs')
    spec = jobs[0]['spec']['template']['spec']
    browsers = [c for c in spec.get('initContainers', []) if c.get('name') == 'chromium']
    if len(browsers) != 1 or browsers[0].get('command', [])[:2] != ['sh', '-c'] or len(browsers[0]['command']) != 3:
        raise ValueError('Public Gateway requires the bounded Chromium fixture')
    browser_container = browsers[0]
    trust_keys = [f'gateway-public-ca-{i}.pem' for i in range(len(CERTIFICATE.findall(config['ca_pem'])))]
    if any(key in maps[0].get('data', {}) for key in trust_keys):
        raise ValueError('Public browser trust is already configured')
    maps[0].setdefault('data', {})['gateway-public.json'] = json.dumps(config, sort_keys=True)
    for key, certificate in zip(trust_keys, CERTIFICATE.findall(config['ca_pem']), strict=True):
        maps[0]['data'][key] = certificate + '\n'
    spec['volumes'] += [
        {'name': 'browser-public-ca', 'configMap': {'name': 'database-ca', 'defaultMode': 0o440,
          'items': [{'key': key, 'path': key} for key in trust_keys]}},
        {'name': 'browser-nss', 'emptyDir': {'sizeLimit': '16Mi'}},
    ]
    browser_container['volumeMounts'] += [
        {'name': 'browser-public-ca', 'mountPath': '/browser-public-ca', 'readOnly': True},
        {'name': 'browser-nss', 'mountPath': '/home/seluser/.pki/nssdb'},
    ]
    # The pinned image already supplies certutil. Keep HOME unchanged and put
    # only this test's declared public roots in an ephemeral browser database.
    browser_container['command'][2] = """set -eu
 test "$HOME" = /home/seluser
 db=sql:/home/seluser/.pki/nssdb
 if [ ! -s /home/seluser/.pki/nssdb/cert9.db ]; then
   timeout 5s certutil -N --empty-password -d "$db"
 fi
 for certificate in /browser-public-ca/*.pem; do
   timeout 5s certutil -A -d "$db" -t 'C,,' -n "${certificate##*/}" -i "$certificate"
 done
 """ + browser_container['command'][2]
    spec['containers'][0]['env'].append({'name': 'STEGO_TEST_BROWSER_PUBLIC_CA_SHA256',
        'value': hashlib.sha256(config['ca_pem'].encode()).hexdigest()})
    spec['volumes'].append({'name': name, 'configMap': {'name': 'database-ca', 'defaultMode': 0o440,
                                                      'items': [{'key': 'gateway-public.json', 'path': 'config.json'}]}})
    test = spec['containers'][0]
    test['volumeMounts'].append({'name': name, 'mountPath': '/gateway-public', 'readOnly': True})
    test['env'] += [{'name': 'STEGO_TEST_GATEWAY_PUBLIC_CONFIG', 'value': '/gateway-public/config.json'},
                    {'name': 'STEGO_TEST_REQUIRE_PUBLIC_GATEWAY', 'value': '1'}]
