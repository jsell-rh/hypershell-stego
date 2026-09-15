"""Read the operator's Kubernetes endpoint snapshot for the browser test.

The installation and the test Job must use the same address set. This module
reads saved API objects. It does not resolve DNS or update network rules.
"""
import ipaddress
import json
from pathlib import Path


def kubernetes_endpoints(directory):
    def read(name):
        with (Path(directory) / name).open('rb') as source:
            raw = source.read((256 << 10) + 1)
        if len(raw) > 256 << 10:
            raise ValueError('The Kubernetes endpoint snapshot exceeds its limit')
        return json.loads(raw)

    def address(value, port):
        ip = ipaddress.ip_address(value)
        if str(ip) == '255.255.255.255' or ip.is_unspecified or ip.is_loopback or ip.is_link_local or ip.is_multicast or getattr(ip, 'ipv4_mapped', None) or '%' in value:
            raise ValueError('The Kubernetes endpoint address is not permitted')
        if type(port) is not int or not 1 <= port <= 65535:
            raise ValueError('The Kubernetes endpoint port is invalid')
        return f'[{ip}]:{port}' if ip.version == 6 else f'{ip}:{port}'

    endpoints = set()
    slices = read('kubernetes-endpoints.json')
    for item in slices['items']:
        for endpoint in item['endpoints']:
            if endpoint.get('conditions', {}).get('ready') is not True:
                continue
            for port in item['ports']:
                if port.get('protocol') != 'TCP' or port.get('name') != 'https':
                    continue
                for value in endpoint['addresses']:
                    endpoints.add(address(value, port['port']))
    if not endpoints:
        raise ValueError('The Kubernetes endpoint snapshot has no ready HTTPS address')
    service = read('kubernetes-service.json')
    for value in service['spec'].get('clusterIPs', [service['spec']['clusterIP']]):
        endpoints.add(address(value, 443))
    if not 1 <= len(endpoints) <= 16:
        raise ValueError('The Kubernetes endpoint set exceeds its limit')
    return sorted(endpoints)


if __name__ == '__main__':
    import sys
    if len(sys.argv) != 2:
        raise SystemExit('Usage: kubernetes_endpoint_bindings.py SNAPSHOT_DIRECTORY')
    print(json.dumps({'kubernetes': kubernetes_endpoints(sys.argv[1])}))
