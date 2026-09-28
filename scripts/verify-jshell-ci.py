#!/usr/bin/env python3
"""Check CI permissions and Job admission without starting a Job."""
import argparse
from pathlib import Path
import copy,json,subprocess,tempfile
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--kubeconfig', required=True, type=Path)
parser.add_argument('--context', required=True)
parser.add_argument('--capacity-kubeconfig',type=Path)
args = parser.parse_args()
root=Path(tempfile.mkdtemp(prefix='stego-ci-identity-'))
prefix=['oc','--kubeconfig='+str(args.kubeconfig),'--context='+args.context,'--request-timeout=15s']
checks=[]
for verb,resource,ns,want in [('create','jobs.batch','stego-ci',True),('get','pods/log','stego-ci',True),('create','pods/exec','stego-ci',True),('patch','leases.coordination.k8s.io/jshell-live-test','stego-ci',True),('create','jobs.batch','default',False),('get','secrets','default',False),('create','clusterroles.rbac.authorization.k8s.io','',False),('patch','validatingadmissionpolicies.admissionregistration.k8s.io','',False),('create','namespaces','',False),('create','rolebindings.rbac.authorization.k8s.io','stego-ci',False),('create','serviceaccounts/token','stego-ci',False),('patch','leases.coordination.k8s.io/other','stego-ci',False),('get','secrets','stego-ci-access',False),('create','jobs.batch','stego-ci-access',False),('create','serviceaccounts/token','stego-ci-access',False),('impersonate','users','',False),('get','secrets/cli-test-postgres','stego-ci',True),('get','secrets/unlisted','stego-ci',False)]:
 cmd=prefix+['auth','can-i',verb,resource]
 if resource in ['pods/log','pods/exec','serviceaccounts/token']:
  parent,subresource=resource.split('/')
  cmd=prefix+['auth','can-i',verb,parent,'--subresource='+subresource]
 if ns:cmd+=['-n',ns]
 r=subprocess.run(cmd,capture_output=True,text=True,timeout=25)
 value=r.stdout.strip()
 if value not in ['yes','no'] or r.returncode!=(0 if value=='yes' else 1) or (value=='yes')!=want:raise RuntimeError('Permission check failed: '+repr((verb,resource,ns,value,r.stderr)))
 checks.append({'verb':verb,'resource':resource,'namespace':ns,'allowed':want})
base={'apiVersion':'batch/v1','kind':'Job','metadata':{'name':'ci-policy-probe','namespace':'stego-ci'},'spec':{'backoffLimit':0,'activeDeadlineSeconds':30,'ttlSecondsAfterFinished':60,'template':{'spec':{'restartPolicy':'Never','automountServiceAccountToken':False,'securityContext':{'runAsNonRoot':True,'seccompProfile':{'type':'RuntimeDefault'}},'containers':[{'name':'check','image':'docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393','command':['/bin/true'],'securityContext':{'allowPrivilegeEscalation':False,'readOnlyRootFilesystem':True,'capabilities':{'drop':['ALL']}},'resources':{'limits':{'cpu':'100m','memory':'64Mi','ephemeral-storage':'16Mi'}}}]}}}}
probes=[]
for name in ['bounded','no_deadline','long_deadline','retry','no_cleanup','automatic_token','projected_token','worker_identity','privileged','host_network','writable_root','no_limits','unlisted_secret_volume','unlisted_secret_env','unlisted_secret_envfrom','unlisted_projected_secret','csi_volume']:
 job=copy.deepcopy(base);spec=job['spec'];pod=spec['template']['spec'];container=pod['containers'][0]
 if name=='no_deadline':spec.pop('activeDeadlineSeconds')
 if name=='long_deadline':spec['activeDeadlineSeconds']=1201
 if name=='retry':spec['backoffLimit']=1
 if name=='no_cleanup':spec.pop('ttlSecondsAfterFinished')
 if name=='automatic_token':pod['automountServiceAccountToken']=True
 if name=='projected_token':pod['volumes']=[{'name':'api','projected':{'sources':[{'serviceAccountToken':{'path':'token'}}]}}]
 if name=='worker_identity':pod['serviceAccountName']='hypershell-ci'
 if name=='privileged':
  container['securityContext']['privileged']=True
  container['securityContext']['allowPrivilegeEscalation']=True
 if name=='host_network':pod['hostNetwork']=True
 if name=='writable_root':container['securityContext']['readOnlyRootFilesystem']=False
 if name=='no_limits':container.pop('resources')
 if name=='unlisted_secret_volume':pod['volumes']=[{'name':'probe','secret':{'secretName':'unlisted'}}]
 if name=='unlisted_projected_secret':pod['volumes']=[{'name':'probe','projected':{'sources':[{'secret':{'name':'unlisted'}}]}}]
 if name=='unlisted_secret_env':container['env']=[{'name':'PROBE','valueFrom':{'secretKeyRef':{'name':'unlisted','key':'probe'}}}]
 if name=='unlisted_secret_envfrom':container['envFrom']=[{'secretRef':{'name':'unlisted'}}]
 if name=='csi_volume':pod['volumes']=[{'name':'probe','csi':{'driver':'fixture.example'}}]
 r=subprocess.run(prefix+['-n','stego-ci','create','--dry-run=server','-f','-','-o','json'],input=json.dumps(job),capture_output=True,text=True,timeout=25)
 want=name=='bounded'
 denial='uses an inline volume provided by CSIDriver fixture.example' if name=='csi_volume' else 'stego-ci-bounded-jobs'
 if (r.returncode==0)!=want or (not want and denial not in r.stderr):raise RuntimeError('Admission check failed: '+name+' '+r.stderr)
 probes.append({'case':name,'allowed':want,'denial_source':None if want else denial})
for kind in ['Opaque', 'kubernetes.io/service-account-token', 'unlisted']:
 secret={'apiVersion':'v1','kind':'Secret','metadata':{'name':'database-tls','namespace':'stego-ci','annotations':{'kubernetes.io/service-account.name':'hypershell-ci'}},'type':kind,'stringData':{'probe':'fixture'}}
 if kind=='unlisted':secret['type']='Opaque';secret['metadata']['name']='unlisted'
 r=subprocess.run(prefix+['-n','stego-ci','create','--dry-run=server','-f','-','-o','json'],input=json.dumps(secret),capture_output=True,text=True,timeout=25)
 want=kind=='Opaque'
 if (r.returncode==0)!=want or (not want and 'stego-ci-no-legacy-tokens' not in r.stderr):raise RuntimeError('Secret admission check failed: '+kind+' '+r.stderr)
 probes.append({'case':kind,'allowed':want})
(root/'verification.json').write_text(json.dumps({'permissions':checks,'admission':probes,'live_jobs_created':0},indent=2)+'\n')
if args.capacity_kubeconfig:
 cap_prefix=['oc','--kubeconfig='+str(args.capacity_kubeconfig),'--context=jshell-capacity','--request-timeout=15s']
 checks_cap=[]
 matrix=[('list','endpointslices.discovery.k8s.io','default',True),('get','services/kubernetes','default',True),('patch','leases.coordination.k8s.io/jshell-live-test','stego-ci',True),('create','namespaces','',True),('create','clusterroles.rbac.authorization.k8s.io','',True),('patch','clusterroles.rbac.authorization.k8s.io','',True),('create','clusterrolebindings.rbac.authorization.k8s.io','',True),('create','validatingadmissionpolicies.admissionregistration.k8s.io','',True),('create','jobs.batch','stego-ci',True),('patch','roles.rbac.authorization.k8s.io','stego-ci',True),('create','serviceaccounts/token','stego-ci',True),('create','pods/exec','stego-ci',True),('get','pods','stego-ci',True),('create','cronjobs.batch','',False),('get','persistentvolumeclaims','default',False),('impersonate','serviceaccounts','',False)]
 for verb,resource,ns,want in matrix:
  cmd=cap_prefix+['auth','can-i',verb,resource]
  if resource in ['pods/log','pods/exec','serviceaccounts/token']:
   parent,subresource=resource.split('/')
   cmd=cap_prefix+['auth','can-i',verb,parent,'--subresource='+subresource]
  if ns:cmd+=['-n',ns]
  r=subprocess.run(cmd,capture_output=True,text=True,timeout=25)
  value=r.stdout.strip()
  if value not in ['yes','no'] or r.returncode!=(0 if value=='yes' else 1) or (value=='yes')!=want:raise RuntimeError('Capacity permission check failed: '+repr((verb,resource,ns,value,r.stderr)))
  checks_cap.append({'verb':verb,'resource':resource,'namespace':ns,'allowed':want})
 cap_probes=[]
 run_name='stego-service-20260927-abc123'
 def cap_create(doc):
  return subprocess.run(cap_prefix+['create','--dry-run=server','-f','-','-o','json'],input=json.dumps(doc),capture_output=True,text=True,timeout=25)
 clusterrole=lambda name:{'apiVersion':'rbac.authorization.k8s.io/v1','kind':'ClusterRole','metadata':{'name':name},'rules':[]}
 namespace=lambda name:{'apiVersion':'v1','kind':'Namespace','metadata':{'name':name}}
 def job(ns):
  doc=copy.deepcopy(base);doc['metadata']['name']='capacity-policy-probe';doc['metadata']['namespace']=ns;return doc
 for case,doc,want,source in [('clusterrole_run_name',clusterrole(run_name),True,None),('clusterrole_foreign_name',clusterrole('kube-root-ca-viewer-2'),False,'stego-ci-capacity-boundary-cluster-objects'),('namespace_run_name',namespace(run_name),True,None),('namespace_foreign_name',namespace('stego-ci-access'),False,'stego-ci-capacity-boundary-namespaces'),('job_in_stego_ci',job('stego-ci'),False,'stego-ci-capacity-boundary-namespaced-writes'),('job_in_default',job('default'),False,'stego-ci-capacity-boundary-namespaced-writes')]:
  r=cap_create(doc)
  if (r.returncode==0)!=want or (not want and source not in r.stderr):raise RuntimeError('Capacity admission probe failed: '+case+' '+r.stderr)
  cap_probes.append({'case':case,'allowed':want,'denial_source':source})
 (root/'capacity-verification.json').write_text(json.dumps({'permissions':checks_cap,'admission':cap_probes,'live_objects_created':0},indent=2)+'\n')
 print(json.dumps({'capacity_permissions_passed':len(checks_cap),'capacity_admission_passed':len(cap_probes)}))
print(root)
print(json.dumps({'permissions_passed':len(checks),'admission_passed':len(probes),'live_jobs_created':0}))
