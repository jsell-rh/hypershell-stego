#!/usr/bin/env python3
"""Render the bounded service fixture without cluster writes."""
import base64
import json
import os
import secrets
import sys
from pathlib import Path
from kubernetes_endpoint_bindings import kubernetes_endpoints
from gateway_endpoint_fixture import inputs as endpoint_inputs

PROJECT = Path(__file__).resolve().parent.parent


def fixture(ns, directory, browser, workload, issuer):
    root = Path(directory)
    if workload not in ('0', '1'):
        raise ValueError('Invalid workload fixture flag')
    job=json.loads((PROJECT / 'acceptance/kubernetes-service-job.json').read_text().replace('@NAMESPACE@',ns))
    if browser not in ('0','1'): raise SystemExit('STEGO_TEST_BROWSER_DEPLOYMENT must be 0 or 1')
    if browser=='1':
        security={'runAsNonRoot':True,'readOnlyRootFilesystem':True,'allowPrivilegeEscalation':False,'capabilities':{'drop':['ALL']}}
        for item in job['items']:
            if item['kind']=='ResourceQuota': item['spec']['hard'].update({'limits.memory':'8Gi','limits.cpu':'8','pods':'6'})
            if item['kind']=='Role' and item['metadata']['name']=='service-check':
                for rule in item['rules']:
                    if 'deployments/scale' in rule['resources']: rule['resourceNames']=['hypershell','hypershell-console','hypershell-provisioner']
            if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
                item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-console'}}}],'ports':[{'port':5432,'protocol':'TCP'},{'port':19093,'protocol':'TCP'}]})
                item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-provisioner'}}}],'ports':[{'port':19093,'protocol':'TCP'},{'port':19094,'protocol':'TCP'}]})
            if item['kind']=='Job':
                spec=item['spec']['template']['spec']
                spec['initContainers'].insert(0,{'name':'node-tools','image':'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393','command':['sh','-c','mkdir -p /work/bin /work/node; cp /usr/local/bin/node /work/bin/node; cp -R /usr/local/lib/node_modules/npm /work/node/npm'],'securityContext':security,'resources':{'requests':{'cpu':'100m','memory':'128Mi'},'limits':{'cpu':'500m','memory':'256Mi','ephemeral-storage':'256Mi'}},'volumeMounts':[{'name':'work','mountPath':'/work'}]})
                spec['initContainers'].append({'name':'chromium','restartPolicy':'Always','image':'docker.io/selenium/standalone-chromium@sha256:81c80050126f610675e40eeac529a821dc5a0d38acf26c6d44f792a6e7ea8ac5','command':['sh','-c','mkdir -p /tmp/config /tmp/cache; exec chromedriver --port=9515 --allowed-ips=127.0.0.1'],'env':[{'name':'XDG_CONFIG_HOME','value':'/tmp/config'},{'name':'XDG_CACHE_HOME','value':'/tmp/cache'}],'securityContext':security,'resources':{'requests':{'cpu':'100m','memory':'256Mi'},'limits':{'cpu':'1','memory':'1536Mi','ephemeral-storage':'1Gi'}},'startupProbe':{'tcpSocket':{'port':9515},'periodSeconds':2,'failureThreshold':30},'volumeMounts':[{'name':'chrometmp','mountPath':'/tmp'}]})
                spec['volumes'].append({'name':'chrometmp','emptyDir':{'sizeLimit':'512Mi'}})
                test=spec['containers'][0]
                test['command'][-1]=test['command'][-1].replace('run-service-deployment-pod.sh','run-browser-deployment-pod.sh')
                test['env'] += [{'name':'STEGO_TEST_KUBERNETES_BROWSER','value':'1'},{'name':'STEGO_REQUIRE_BROWSER','value':'1'},{'name':'PATH','value':'/work/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'}]
    if workload=='1':
        import re
        if browser!='1' or not re.fullmatch(r'[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?',issuer): raise SystemExit('Invalid Gateway test profile')
        endpoints = kubernetes_endpoints(root)
        change = endpoint_inputs(PROJECT, root, ns)
        for item in job['items']:
            if item['kind']=='ResourceQuota': item['spec']['hard'].update({'limits.memory':'11Gi','limits.cpu':'12','pods':'10'})
            if item['kind']=='Role' and item['metadata']['name']=='service-check':
                for rule in item['rules']:
                    if 'deployments/scale' in rule['resources']: rule['resourceNames'] += ['hypershell-namespace-allocation','hypershell-gateway-identity','hypershell-gateway-workload']
                item['rules'].append({'apiGroups':[''],'resources':['serviceaccounts/token'],'resourceNames':['hypershell-namespace-allocation','hypershell-gateway-identity','hypershell-gateway-workload'],'verbs':['create']})
            if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
                for worker in ['namespace-allocation','gateway-identity','gateway-workload']:
                    item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-'+worker}}}],'ports':[{'port':19093,'protocol':'TCP'}]})
            if item['kind']=='Job': item['spec']['template']['spec']['containers'][0]['env'].append({'name':'STEGO_TEST_KUBERNETES_EGRESS','value':json.dumps(sorted(endpoints))})
            if item['kind'] == 'Job' and change:
                item['spec']['template']['spec']['containers'][0]['env'] += [
                    {'name': 'STEGO_TEST_GATEWAY_ENDPOINT_CHANGE', 'value': json.dumps(change)},
                    {'name': 'STEGO_TEST_ALLOCATION_NETWORK_ENDPOINTS', 'value': json.dumps({'kubernetes': sorted(endpoints), 'network-probe': [change['initial']]})}]
        import hashlib
        marker=hashlib.sha256((ns+'.hypershell-namespace-allocation').encode()).hexdigest()[:32]
        for item in job['items']:
            if item['kind']=='NetworkPolicy' and item['metadata']['name']=='fixture-ingress':
                item['spec']['ingress'].append({'from':[{'podSelector':{'matchLabels':{'app.kubernetes.io/name':'hypershell-gateway-workload'}}}],'ports':[{'port':5432,'protocol':'TCP'}]})
                item['spec']['ingress'].append({'from':[{'namespaceSelector':{'matchLabels':{'stego.dev/allocator':marker,'stego.dev/allocation-profile':'gateway'}}}],'ports':[{'port':5432,'protocol':'TCP'},{'port':19093,'protocol':'TCP'}]})
        role=json.loads((PROJECT / 'acceptance/browser-workload-rbac.json').read_text().replace('@NAMESPACE@',ns).replace('@ALLOCATOR_MARKER@',marker))
        job['items'] += role['items']
        for item in job['items']:
            if item['kind']=='Job': item['spec']['template']['spec']['containers'][0]['env'] += [{'name':'STEGO_TEST_BROWSER_WORKLOAD','value':'1'},{'name':'STEGO_TEST_GATEWAY_CLUSTER_ISSUER','value':issuer}]
        if ns != 'stego-service-ci':
            from network_peer_fixture import peer_namespace
            host = 'peer.' + peer_namespace(ns) + '.svc.cluster.local'
            for item in job['items']:
                if item['kind'] == 'Job':
                    item['spec']['template']['spec']['containers'][0]['env'].append({'name': 'STEGO_TEST_UNRELATED_NETWORK_HOST', 'value': host})
    from public_gateway_fixture import apply_public_fixture
    apply_public_fixture(job, ns, workload, browser)
    from internal_gateway_fixture import apply_internal_fixture
    apply_internal_fixture(job, workload, browser)
    for item in job['items']:
        if item['kind'] == 'Role' and item['metadata']['name'] == 'service-check':
            for rule in list(item['rules']):
                if rule['apiGroups'] == [''] and 'serviceaccounts' in rule['resources']:
                    rule['resources'].remove('serviceaccounts')
                    item['rules'].append({'apiGroups': [''], 'resources': ['serviceaccounts'], 'verbs': [v for v in rule['verbs'] if v != 'delete']})
        if item['kind'] == 'Job':
            item['spec']['ttlSecondsAfterFinished'] = 3600
            item['spec']['template']['spec'].setdefault('securityContext', {}).update({'runAsNonRoot': True, 'seccompProfile': {'type': 'RuntimeDefault'}})
        if item['kind'] in {'Secret', 'ConfigMap', 'Service', 'Job'}:
            item['metadata'].setdefault('labels', {})['stego.test/browser-run'] = ns
    return job


def main():
    if len(sys.argv) != 6:
        raise SystemExit('Require namespace, output directory, browser flag, workload flag, and issuer')
    ns, root = sys.argv[1], Path(sys.argv[2])
    job = fixture(ns, root, *sys.argv[3:])
    password=secrets.token_hex(24)
    encode=lambda value:base64.b64encode(value.encode()).decode()
    for item in job['items']:
        if item['kind']=='Secret' and item['metadata']['name']=='cli-test-postgres':
            item['data']={key:encode(value) for key,value in {
              'password':password,
              'dsn':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt',
              'url':f'postgres://postgres:{password}@127.0.0.1:5432/postgres?sslmode=verify-full&sslrootcert=/tls/server.crt'
            }.items()}
        if item['kind']=='Secret' and item['metadata']['name']=='database-tls':
            item['data']={name:encode((root/name).read_text()) for name in ['server.key','server.crt']}
        if item['kind']=='ConfigMap' and item['metadata']['name']=='database-ca':
            item.setdefault('data', {})['server.crt']=(root/'ca.crt').read_text()
    (root/'private-job.json').write_text(json.dumps(job))
    for item in job['items']:
        if item['kind']=='Secret':item.pop('data',None)
    (root/'job.json').write_text(json.dumps(job,indent=2)+'\n')


if __name__ == '__main__':
    main()
