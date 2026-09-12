import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';

const [inputPath,outputPath,phase]=process.argv.slice(2);
const input=JSON.parse(await readFile(inputPath,'utf8'));
const key='element-6066-11e4-a52e-4f735466cecf';
let session=input.session,id=input.id;
const accountName='rendered-account-lifecycle';
async function request(path,body,method=body===undefined?'GET':'POST'){
 const response=await fetch('http://127.0.0.1:9515'+path,{method,headers:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)}),signal:AbortSignal.timeout(20000)});
 const data=await response.json();if(!response.ok||data.value?.error)throw new Error(`WebDriver failed: ${data.value?.error??response.status}`);return data.value;
}
const command=(path,body,method)=>request(`/session/${session}${path}`,body,method);
const script=(source,args=[])=>command('/execute/sync',{script:source,args});
async function until(check,label){const end=Date.now()+20000;while(Date.now()<end){const value=await check();if(value)return value;await new Promise(resolve=>setTimeout(resolve,100));}throw new Error(`Account browser did not reach ${label}`);}
async function element(selector){return until(async()=>{const values=await command('/elements',{using:'css selector',value:selector});return values[0]?.[key];},selector);}
const click=async selector=>command(`/element/${await element(selector)}/click`,{});
const type=async(selector,text)=>command(`/element/${await element(selector)}/value`,{text});
async function textButton(text,selector='button'){
 const found=await until(()=>script('return [...document.querySelectorAll(arguments[1])].find(e=>e.textContent.trim()===arguments[0])',[text,selector]),text);
 return command(`/element/${found[key]}/click`,{});
}
async function readAccounts(suffix=''){
 return command('/execute/async',{script:'const done=arguments[arguments.length-1];fetch(arguments[0],{signal:AbortSignal.timeout(4000)}).then(async r=>done({status:r.status,body:await r.json()})).catch(()=>done({status:0}));',args:[`/api/hypershell/v1/gateways/${input.gateway}/service_accounts${suffix}`]});
}
const rowActions=()=>click(`[aria-label="Actions for service account ${accountName}"]`);
async function accountsTab(){await textButton('Service accounts','[role="tab"]');await until(()=>script('return document.body.innerText.includes("Service accounts")'),'accounts page');}
try{
 if(phase==='close'){if(session){try{await command('',undefined,'DELETE');}catch(error){if(!error.message.includes('invalid session id'))throw error;}}process.exit(0);}
 if(phase==='create'){
  const created=await request('/session',{capabilities:{alwaysMatch:{browserName:'chrome','goog:chromeOptions':{binary:process.env.STEGO_TEST_CHROMIUM_BINARY??'/usr/bin/chromium',args:['--headless=new','--disable-gpu','--no-sandbox','--disable-dev-shm-usage','--disable-background-networking','--no-first-run','--window-size=1280,960',`--user-data-dir=/tmp/stego-accounts-${Date.now()}`,`--ignore-certificate-errors-spki-list=${input.pins.join(',')}`]}}}});
  session=created.sessionId;await command('/timeouts',{implicit:0,pageLoad:20000,script:5000});
  await command('/url',{url:input.origin+'/auth/login?return_to='+encodeURIComponent('/gateways/'+input.gateway)});
  await type('#username','console-alice');await type('#password','acceptance-only-user-password');await click('#kc-login');
  await until(()=>script('return document.body.innerText.includes(arguments[0])',[input.gatewayName]),'fixture Gateway');
  await accountsTab();await textButton('Create service account');await type('#service-account-name',accountName);
  await click('button[aria-label="OpenShell role"]');
  const option=await until(()=>script('return [...document.querySelectorAll("button")].find(e=>e.textContent.startsWith("openshell-admin"))'),'admin role');await command(`/element/${option[key]}/click`,{});
  await textButton('Create service account','[role="dialog"] button');
  await element('input[aria-label="Client secret"]');
  assert.ok(await script('return document.querySelector("input[aria-label=\\"Client secret\\"]").type==="password"'),'client secret must be masked');
  assert.ok(await script('return [...document.querySelectorAll("button")].find(e=>e.textContent.trim()==="Finish setup").disabled'),'one-time handoff requires acknowledgement');
  const secret=await script('return document.querySelector("input[aria-label=\\"Client secret\\"]").value');
  assert.ok(typeof secret==='string'&&secret.length>=16,'one-time credential missing');
  const result=await readAccounts();assert.ok(result.status===200,'account list failed');
  const account=result.body.items.find(row=>row.name===accountName);assert.ok(account?.id&&account.client_id,'created account metadata missing');id=account.id;
  // This file is outside the retained evidence directory and is removed by Go.
  await writeFile(input.privateFile,JSON.stringify({id,client_id:account.client_id,secret}),{mode:0o600,flag:'wx'});
  await click('[role="dialog"] input[type="checkbox"]');await textButton('Finish setup');
  await command('/refresh',{});await until(()=>script('return document.body.innerText.includes(arguments[0])',[input.gatewayName]),'Gateway reload');await accountsTab();
  assert.ok(!await script('return !!document.querySelector("input[aria-label=\\"Client secret\\"]")'),'secret survived handoff');
  const detail=await readAccounts('/'+id);assert.ok(detail.status===200,'account detail failed');
  assert.ok(!JSON.stringify(detail.body).includes(secret)&&!JSON.stringify(detail.body).includes('"client_secret":'),'detail exposed a credential');
 }else if(phase==='revoke'){
  await rowActions();await textButton('View setup instructions');
  await until(()=>script('return document.body.innerText.includes("The client secret is no longer available")'),'non-secret setup');
  assert.ok(!await script('return !!document.querySelector("input[aria-label=\\"Client secret\\"]")'),'setup restored the secret');
  await textButton('Close setup instructions');
  await rowActions();await textButton('Revoke service account');await textButton('Cancel','[role="dialog"] button');
  let detail=await readAccounts('/'+id);assert.ok(detail.status===200&&detail.body.status==='ready','cancelled revoke changed the account');
  await rowActions();await textButton('Revoke service account');await textButton('Revoke service account','[role="dialog"] button');
  await until(async()=>{const value=await readAccounts('/'+id);return value.status===200&&value.body.status==='revoked';},'revocation');
 }else if(phase==='delete'){
  await rowActions();await textButton('Delete service account');await textButton('Delete service account','[role="dialog"] button');
  await until(async()=>{const value=await readAccounts();return value.status===200&&!value.body.items.some(row=>row.id===id);},'deletion');
  await until(()=>script('return document.body.innerText.includes("No service accounts")'),'empty account list');
  await writeFile(outputPath+'.png',Buffer.from(await command('/screenshot'),'base64'));
 }else throw new Error('unknown account phase');
 await writeFile(outputPath,JSON.stringify({session,id}));
}catch(error){
 // Do not capture the DOM or a screenshot while it can contain a one-time secret.
 if(session)await command('',undefined,'DELETE').catch(()=>{});
 throw error;
}
