import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';

// W3C WebDriver controls a bounded browser in the cluster fixture.
const [inputPath,outputPath,phase='create'] = process.argv.slice(2);
const input=JSON.parse(await readFile(inputPath,'utf8'));
const base='http://127.0.0.1:9515';
const elementKey='element-6066-11e4-a52e-4f735466cecf';
let session=input.session;
async function request(path,body,method=body===undefined?'GET':'POST') {
 const response=await fetch(base+path,{method,headers:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)}),signal:AbortSignal.timeout(20000)});
 const data=await response.json();
 if(!response.ok||data.value?.error) throw new Error(`WebDriver ${path}: ${data.value?.error??response.status}`);
 return data.value;
}
const command=(path,body,method)=>request(`/session/${session}${path}`,body,method);
async function until(check,label) {
 const deadline=Date.now()+20000;
 while(Date.now()<deadline){const value=await check();if(value)return value;await new Promise(resolve=>setTimeout(resolve,100));}
 throw new Error(`Browser did not reach ${label}`);
}
async function element(selector){return until(async()=>{const all=await command('/elements',{using:'css selector',value:selector});return all[0]?.[elementKey];},selector);}
const click=async selector=>command(`/element/${await element(selector)}/click`,{});
const type=async(selector,text)=>command(`/element/${await element(selector)}/value`,{text});
const script=(source,args=[])=>command('/execute/sync',{script:source,args});
async function newSession(){
 const value=await request('/session',{capabilities:{alwaysMatch:{browserName:'chrome','goog:loggingPrefs':{performance:'ALL',browser:'ALL'},'goog:chromeOptions':{binary:process.env.STEGO_TEST_CHROMIUM_BINARY??'/usr/bin/chromium',args:['--headless=new','--disable-gpu','--no-sandbox','--disable-dev-shm-usage','--disable-background-networking','--no-first-run','--window-size=1280,960',`--user-data-dir=/tmp/stego-chrome-${Date.now()}`,`--ignore-certificate-errors-spki-list=${input.pins.join(',')}`]}}}});
 session=value.sessionId;await command('/timeouts',{implicit:0,pageLoad:20000,script:5000});
}
async function login(username){
 await command('/url',{url:input.origin+'/auth/login?return_to=%2Fgateways%2Fnew'});
 await type('#username',username);await type('#password','acceptance-only-user-password');await click('#kc-login');
 await element('#gateway-name');
}
try {
 if(phase==='close'){try{await command('',undefined,'DELETE');}catch(error){if(!error.message.includes('invalid session id'))throw error;}process.exit(0);}
 if(phase==='create'){
  await newSession();await login('console-alice');
  const config=await script('return JSON.parse(document.querySelector(\'meta[name="stego-runtime-config"]\').content)');
  assert.equal(config.version,1);assert.equal(config.traces,true);assert.equal(config.logs,true);assert.equal(config.metrics,true);
  await type('#gateway-name','rendered-browser-workflow');
  await click('#gateway-cluster');
  await until(()=>script('return document.getElementById("gateway-cluster-option-1")?.textContent.startsWith("cluster")'),'cluster option');
  await click('#gateway-cluster-option-1');
  await click('button[type="submit"]');
  const url=await until(async()=>{const value=await command('/url');return /\/gateways\/[0-9A-Za-z]{27}$/.test(value)?value:undefined;},'Gateway detail');
  const id=new URL(url).pathname.split('/').at(-1);
  await until(()=>script('return document.body.innerText.includes("rendered-browser-workflow")'),'Gateway name');
  await script('window.dispatchEvent(new Event("pagehide"))');
  await writeFile(outputPath,JSON.stringify({id,session}));
 }else if(phase==='reload'){
  await command('/refresh',{});
  await until(()=>script('return document.body.innerText.includes("rendered-browser-workflow")'),'Gateway after cold reload');
  await writeFile(outputPath,JSON.stringify({reloaded:true}));
 }else if(phase==='verify'){
  await command('/log',{type:'performance'});
  await command('/refresh',{});
  await until(()=>script('return document.body.innerText.includes("rendered-browser-workflow")'),'Gateway with collector failure');
  await script('window.dispatchEvent(new Event("pagehide"))');
  const failedSignals=new Set();
  await until(async()=>{
   for(const entry of await command('/log',{type:'performance'})){
    const event=JSON.parse(entry.message).message;
    if(event.method==='Network.responseReceived'){
     const response=event.params.response;
     if(response.url.startsWith(input.origin+'/telemetry/v1/')&&response.status===503)failedSignals.add(new URL(response.url).pathname);
    }
   }
   return ['traces','logs','metrics'].every(signal=>failedSignals.has('/telemetry/v1/'+signal));
  },'bounded telemetry failure');
  await until(()=>script('return document.body.innerText.includes("rendered-browser-workflow")'),'Gateway after restart');
  await command('/url',{url:input.origin+'/'});
  await element(`a[href="/gateways/${input.id}"]`);
  await click(`a[href="/gateways/${input.id}"]`);
  await until(()=>script('return document.body.innerText.includes("rendered-browser-workflow")'),'Gateway from list');
  await writeFile(outputPath+'.png',Buffer.from(await command('/screenshot'),'base64'));
  await command('',undefined,'DELETE');session=undefined;
  await newSession();await login('console-bob');
  await type('#gateway-name','denied-rendered-workflow');await click('button[type="submit"]');
  await until(()=>script('return document.body.innerText.includes("Gateway could not be provisioned")'),'denied creation');
  await command('/url',{url:input.origin+'/'});
  await until(()=>script('return document.body.innerText.includes("No gateways")'),'filtered list');
  assert.equal(await script('return document.querySelector(arguments[0])!==null',[`a[href="/gateways/${input.id}"]`]),false);
  await command('/url',{url:input.origin+'/gateways/'+input.id});
  await until(()=>script('return document.body.innerText.match(/not found|could not|unable|unavailable/i)?.[0]'),'denied detail');
  assert.equal(await script('return document.body.innerText.includes("rendered-browser-workflow")'),false);
  await command('',undefined,'DELETE');session=undefined;
  await writeFile(outputPath,JSON.stringify({verified:true}));
 }else throw new Error('unknown browser phase');
}catch(error){
 if(session){
  const responses=[];
  for(const entry of await command('/log',{type:'performance'}).catch(()=>[])){
   const event=JSON.parse(entry.message).message;
   if(event.method==='Network.responseReceived'){
    const response=event.params.response;const url=new URL(response.url);
    if(url.origin===input.origin)responses.push({path:url.pathname,status:response.status,type:response.mimeType});
   }
  }
  await writeFile(outputPath+'.network.json',JSON.stringify(responses));
  await writeFile(outputPath+'.txt',String(await script('return document.body.innerText').catch(()=>'')));
  await writeFile(outputPath+'.png',Buffer.from(await command('/screenshot').catch(()=>''),'base64'));
  await command('',undefined,'DELETE').catch(()=>{});
 }
 throw error;
}
