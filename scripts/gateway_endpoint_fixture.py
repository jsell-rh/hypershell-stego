"""Read fixed address-test inputs and check the operator's policy change.

The test Pod cannot supply addresses or policy manifests to the operator.
These checks use the frozen source and the operator's listener record.
"""
import copy
import ipaddress
import json
from pathlib import Path
import re

from network_peer_fixture import peer_namespace


def read_json(path):
    with Path(path).open('rb') as source:
        data = source.read((256 << 10) + 1)
    if len(data) > 256 << 10:
        raise ValueError('The endpoint fixture record exceeds its limit')
    return json.loads(data)


def enabled(source):
    record = Path(source) / 'acceptance/browser-inspection-source.json'
    if not record.exists():
        return False
    value = read_json(record)
    declaration = value.get('network_endpoint_change')
    if declaration is None:
        return False
    if (not isinstance(declaration, dict) or declaration.get('endpoint') != 'network-probe'
            or value.get('cnpg_installation')):
        raise ValueError('The endpoint fixture declaration differs')
    return True


def address(value):
    if not isinstance(value, str) or not value.endswith(':8080'):
        raise ValueError('The endpoint fixture requires TCP port 8080')
    host = value[:-5]
    if host.startswith('[') and host.endswith(']'):
        host = host[1:-1]
    ip = ipaddress.ip_address(host)
    canonical = ('[' + str(ip) + ']' if ip.version == 6 else str(ip)) + ':8080'
    if (value != canonical or ip.is_unspecified or ip.is_loopback or ip.is_link_local or
            ip.is_multicast or getattr(ip, 'ipv4_mapped', None) or '%' in host or
            str(ip) == '255.255.255.255'):
        raise ValueError('The endpoint fixture address is not permitted')
    return {'cidr': str(ip) + ('/128' if ip.version == 6 else '/32'), 'port': '8080'}


def inputs(source, directory, control):
    if not enabled(source):
        return None
    namespace = peer_namespace(control)
    record = read_json(Path(directory) / 'network-peer.json')
    if (record.get('control') != control or record.get('namespace') != namespace or
            record.get('phase') != 'ready' or record.get('endpoint_change') is not True or
            record.get('ingress_policy') is not False or not record.get('namespace_uid') or
            not re.fullmatch('[0-9a-f]{32}', record.get('nonce', ''))):
        raise ValueError('The endpoint listener record is not ready or has another owner')
    listeners = record.get('address_listeners', {})
    if set(listeners) != {'address-a', 'address-b'}:
        raise ValueError('Both endpoint listeners are required')
    values, identities = [], []
    for name in ('address-a', 'address-b'):
        item = listeners[name]
        uid = item.get('uid')
        if not isinstance(uid, str) or not uid or uid != record.get('pods', {}).get(name):
            raise ValueError('The endpoint listener identity differs')
        address(item.get('address'))
        values.append(item['address'])
        identities.append(uid)
    if len(set(values)) != 2 or len(set(identities)) != 2:
        raise ValueError('The endpoint listeners must have distinct addresses and identities')
    return {'endpoint': 'network-probe', 'initial': values[0], 'replacement': values[1],
            'nonce': record['nonce'], 'namespace': namespace, 'namespace_uid': record['namespace_uid']}


def verify_policy_change(before, after, initial, replacement):
    """Accept one address replacement in the Gateway admission variable only."""
    old, new = copy.deepcopy(before), copy.deepcopy(after)
    if old.get('kind') != 'ValidatingAdmissionPolicy' or new.get('kind') != old['kind']:
        raise ValueError('An endpoint change must update an admission policy')
    old_address, new_address = address(initial), address(replacement)
    if old_address == new_address:
        raise ValueError('The endpoint replacement must change the address')
    def variable(item):
        matches = [v for v in item['spec']['variables'] if v.get('name') == 'networkEndpoints']
        if len(matches) != 1:
            raise ValueError('The endpoint admission variable is missing or repeated')
        value = json.loads(matches[0]['expression'])
        if set(value) != {'gateway'} or not isinstance(value['gateway'], list):
            raise ValueError('The endpoint admission profile differs')
        matches[0]['expression'] = 'CHECKED_ENDPOINT_CHANGE'
        return value['gateway']
    old_peers, new_peers = variable(old), variable(new)
    if (old_peers.count(old_address) != 1 or new_address in old_peers or
            new_peers.count(new_address) != 1 or old_address in new_peers):
        raise ValueError('The admission policy did not replace one exact address')
    old_peers.remove(old_address)
    new_peers.remove(new_address)
    if old != new or old_peers != new_peers:
        raise ValueError('The endpoint change modified another admission field or address')


def policy_change(before, after, initial, replacement):
    """Find the single checked policy change in two generated cluster lists."""
    def objects(document):
        if (document.get('apiVersion') != 'v1' or document.get('kind') != 'List' or
                not isinstance(document.get('items'), list) or len(document['items']) > 64):
            raise ValueError('The endpoint render is not a bounded Kubernetes list')
        result = {}
        for item in document['items']:
            key = (item['kind'], item['metadata']['name'])
            if key in result:
                raise ValueError('The endpoint render has repeated resource identities')
            result[key] = item
        return result
    old, new = objects(before), objects(after)
    if old.keys() != new.keys():
        raise ValueError('The endpoint change added or removed a cluster resource')
    changed = [key for key in old if old[key] != new[key]]
    if len(changed) != 1:
        raise ValueError('The endpoint change must update exactly one cluster resource')
    key = changed[0]
    verify_policy_change(old[key], new[key], initial, replacement)
    return old[key], new[key]
