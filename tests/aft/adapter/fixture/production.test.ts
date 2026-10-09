import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { ComposeFixtureDriver, type ProductionConfig, type ProcessRequest } from './production.js';
import { FixtureLifecycle, FixtureError, type FixturePlan } from './lifecycle.js';
import { materializeRenderer } from './renderer-fixtures.test.js';
const hash = (v: string | Uint8Array) => createHash('sha256').update(v).digest('hex');
async function setup(profile = 'agents-real-opencode',fixtureRunId?:string) {
 const root = await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname), 'test-artifacts-'));
 const source = path.join(root,'source'), build = path.join(root,'build');
 for (const dir of [source,build,path.join(root,'home'),path.join(root,'locks')]) await fs.mkdir(dir);
 await fs.writeFile(path.join(source,'tracked'),'source');
 const digest = (es: {relativePath:string;sha256:string}[]) => hash([...es].sort((a,b)=>a.relativePath<b.relativePath?-1:1).map(e=>`${e.sha256}  ${e.relativePath}\n`).join(''));
 const se = [{relativePath:'tracked',sha256:hash('source')}];
 const image = 'sha256:'+'f'.repeat(64), receipt = JSON.stringify({imageId:image,sourceManifestSha256:digest(se)});
 await fs.writeFile(path.join(build,'container-image.json'),receipt);
 const elf=Buffer.from([0x7f,0x45,0x4c,0x46,1,2]); await fs.writeFile(path.join(build,'emulator'),elf);
 const cloud=profile==='legacy-real-codex-podman';
 const be=[{relativePath:'container-image.json',sha256:hash(receipt)},{relativePath:'emulator',sha256:hash(elf)}];
 const auth=path.join(root,'home','auth');await fs.mkdir(auth);await fs.writeFile(path.join(auth,'auth.json'),'private auth');
 const frontend=path.join(build,'frontend');await fs.mkdir(frontend);await fs.writeFile(path.join(frontend,'index.html'),'built frontend');
 const cloudReceipt=JSON.stringify({images:Object.fromEntries(['fleet-db','loom-serve','worker','stub-upstream'].map(s=>[s,image])),sourceManifestSha256:digest(se),fleetSourceManifestSha256:digest(se)});
 await fs.writeFile(path.join(build,'modecloud-images.json'),cloudReceipt);be.push({relativePath:'modecloud-images.json',sha256:hash(cloudReceipt)},{relativePath:'frontend/index.html',sha256:hash('built frontend')});
 const revision={repository:'compose-test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:digest(se),buildManifestSha256:digest(be)};
 const registered={revision,source:{root:source,entries:se},build:{root:build,entries:be}};
 await materializeRenderer(registered,'frontend');
 for(const name of ['container-image.json','modecloud-images.json']){
  const filename=path.join(build,name),data=JSON.parse(await fs.readFile(filename,'utf8'));data.sourceManifestSha256=revision.sourceManifestSha256;
  if(name==='modecloud-images.json')data.fleetSourceManifestSha256=revision.sourceManifestSha256;
  const bytes=JSON.stringify(data);await fs.writeFile(filename,bytes);be.find(e=>e.relativePath===name)!.sha256=hash(bytes);
 }
 revision.buildManifestSha256=digest(be);
 const connection={Identity:'/owned/key',Name:'owned',URI:'ssh://owned'};
 const config:ProductionConfig={loom:registered,fleet:registered,engine:registered,adapter:registered,tempParent:root,lockParent:path.join(root,'locks'),hostHome:path.join(root,'home'),toolPath:'/pinned/toolchain',connection:'owned',connectionFingerprint:hash(JSON.stringify([connection])),minimumFreeBytes:1,fixtureRunId,attestedImages:true,emulatorBinary:{path:path.join(build,'emulator'),sha256:hash(elf)},modecloud:{codexAuthRoot:auth,frontendDist:frontend}};
 const plan:FixturePlan={profile,loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,model:profile==='agents-emulator'?'aft/m':'openai/m',maxCases:10,caseCount:1,selectionSha256:'d'.repeat(64),leaseDurationMs:10000};
 let onExec:((request:ProcessRequest)=>Promise<void>)|undefined;
 const calls:ProcessRequest[]=[]; let project='',up=false,change='',port=5000,serial=0;
 const services=cloud?['redis','fleet-auth-seed','fleet-db','loom-serve','worker','stub-upstream']:['redis','fleet-db','loom-local','ui-local'];
 const run=async (r:ProcessRequest)=>{
  calls.push(r); const a=r.args;
  if(r.binary==='git') return a[0]==='rev-parse'?(a[1]==='HEAD'?revision.commit:revision.tree):a[0]==='ls-files'?se.map(e=>e.relativePath).join('\0')+'\0':'';
  project=r.env.LOCAL_MODE_COMPOSE_PROJECT||r.env.LOOM_STACK_PROJECT||project;
  if(r.binary==='bash') return '';
  if(a[0]==='system') return JSON.stringify([{...connection,URI:change==='connection'?'ssh://foreign':connection.URI}]);
  if(a.includes('image')) return JSON.stringify([{Id:image}]);
  if(a.includes('compose')) { if(a.includes('up')) {up=true;if(change==='fail-up')throw new Error('Bearer private-up-token');} if(a.includes('down')) {if(change==='fail-down')throw new Error('secret=private-down-token');up=false;}return ''; }
  if(a.includes('logs'))return change==='embedded'?'embedded fleet-db started':'opened cloud fleet-db client';
  if(a.includes('exec')){await onExec?.(r);if(a.at(-1)?.includes('runtime-identity')){
    const override=JSON.parse(await fs.readFile(path.join(driver.runtimeRoot,'compose.json'),'utf8'));
    const service=override.services[cloud?'loom-serve':'loom-local'];return JSON.stringify({runId:change==='wrong-run-id'?'foreign-run':service.environment.RUN_ID,leaseId:change==='wrong-run-lease'?'foreign-lease':service.environment.AFT_FIXTURE_LEASE_ID});
  }return a.at(-1)?.includes('controlled-codex-preflight') ? JSON.stringify({ready:true,cleaned:change!=='probe-leak',complete:change!=='probe-incomplete'}) : JSON.stringify({sourceRepo:'/work/source-repos/aft-repo'});}
  if(a.includes('ps')) return up?services.map(s=>`container-${s}`).join('\n'):'';
  if(a.includes('ls')) return up?(a.includes('volume')?'volume-owned':'network-owned'):'';
  if(a.includes('inspect')) {
   const id=a.at(-1)!,container=id.startsWith('container-'),service=id.replace('container-','');
   const labels={'com.docker.compose.project':project,'io.loom.aft.lease':change==='foreign'?'foreign':'opaque-fixture','com.docker.compose.service':service};
   const mappings:Record<string,number>=cloud?{'loom-serve':0,'fleet-db':1,'stub-upstream':2}:{'fleet-db':0,'loom-local':1,'ui-local':2};const index=mappings[service] ?? 0;
   return JSON.stringify([container?{Id:id,Image:change==='wrong-image'?'sha256:'+'e'.repeat(64):image,Config:{Labels:labels},State:{StartedAt:change==='stale'?'new':'generation',Pid:123,Status:service==='fleet-auth-seed'?'exited':'running',ExitCode:0,Health:{Status:change==='unhealthy'?'unhealthy':'healthy'}},Mounts:[{Destination:'/work',Type:change==='hostbind'?'bind':'volume',Name:'volume-owned'},{Destination:'/home/node/.codex',Source:auth,RW:change==='writable-auth'},{Destination:'/opt/webui',Source:frontend,RW:false},{Destination:'/srv',Source:frontend,RW:false}],NetworkSettings:{Ports:{'8080/tcp':[{HostPort:String(change==='wrong-port'?5999:5000+index)}]}}}:{Id:id,Name:id,Labels:labels,CreatedAt:'created'}]);
  }return '';
 };
 const files={...fs,statfs:async()=>({type:0,blocks:20*1024**3,bavail:20*1024**3,bfree:20*1024**3,bsize:1,files:1000,ffree:1000})} as unknown as typeof fs;
 const http=async (_origin:string,relative:string)=>relative.endsWith('/repos')?{success:true,repos:[{name:'aft-repo'}]}:relative.endsWith('/LOCALMODE')?{data:{id:'LOCALMODE',path:'/root/.loom/workspaces/LOCALMODE',repos:[{name:'source-repo',path:'/root/.loom/workspaces/LOCALMODE/source-repo'}]}}:relative.endsWith('/models')?{providers:[{models:[{id:plan.model},{id:'openai/alternate'}]}]}:{};
 const driver=new ComposeFixtureDriver(config,run,files,()=>`id${++serial}`,async()=>({port:port++,async release(){}}),http,async()=>({status:201,body:{}}));
 const lifecycle=new FixtureLifecycle([plan],()=>driver,()=>1000,()=> 'opaque-fixture');
 const request={runId:'run',profile,loomRevision:revision,fleetRevision:revision,model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256};
 return {root,source,driver,lifecycle,request,calls,onExec(callback:(request:ProcessRequest)=>Promise<void>){onExec=callback;},mutate(v:string){change=v;},async cleanup(){await fs.rm(root,{recursive:true});}};
}
for(const profile of ['agents-real-opencode','agents-emulator']) test(`${profile}: concrete compose driver preserves profile realness and resource ownership`,async()=>{
 const r=await setup(profile);try{
  const a=await r.lifecycle.acquire(r.request,new AbortController().signal);assert.equal(a.workspaceId,'LOCALMODE');
  const o=JSON.parse(await fs.readFile(path.join(r.driver.runtimeRoot,'compose.json'),'utf8'));
  assert.equal(o.services['fleet-db'].environment.FLEET_RATE_LIMIT_ENABLED,'false');assert.equal(o.services['loom-local'].labels['io.loom.aft.lease'],a.lease.id);
  if(profile==='agents-emulator')assert.equal(o.services['loom-local'].environment.LOOM_OPENCODE_BIN,'/opt/fixture/loom-harness-emu');
  assert.equal(r.calls.some(c=>c.binary==='bash'&&c.args.includes('make')),profile==='agents-real-opencode');assert.ok(r.calls.some(c=>c.args.includes('up')&&c.args.includes('--no-build')));
  assert.equal((await r.lifecycle.observe(a.lease.id,'run')).services.length,4);const released=await r.lifecycle.release(a.lease.id,'run');assert.equal(released.released,true);assert.ok(await fs.readFile(released.receipt.id));
 }finally{await r.cleanup();}
});
test('partial compose startup cleans only labeled resources and retains safe evidence',async()=>{
 const r=await setup();r.mutate('fail-up');try{
  await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal),e=>{assert.ok(e instanceof FixtureError);assert.equal(e.remainingOwnedResources.length,0);return true;});
  const dir=path.join(r.driver.runtimeRoot,'evidence'),files=await fs.readdir(dir);assert.ok(files.some(f=>f.startsWith('failure-')));
  const contents=await Promise.all(files.map(f=>fs.readFile(path.join(dir,f),'utf8')));assert.equal(contents.join('').includes('private-up-token'),false);assert.ok(r.calls.some(c=>c.args.includes('down')));
 }finally{await r.cleanup();}
});
for(const change of ['foreign','stale','wrong-port','wrong-image','connection'])test(`${change}: uncertain compose inventory cannot authorize cleanup`,async()=>{
 const r=await setup();try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);r.mutate(change);await assert.rejects(r.lifecycle.observe(a.lease.id,'run'));const released=await r.lifecycle.release(a.lease.id,'run');assert.equal(released.released,false);assert.ok(released.remainingOwnedResources.length);assert.equal(r.calls.some(c=>c.args.includes('down')),false);assert.ok(await fs.readFile(released.receipt.id));}finally{await r.cleanup();}
});
test('failed compose teardown retains account locks and exact resources for retry',async()=>{
 const r=await setup();try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);r.mutate('fail-down');const failed=await r.lifecycle.release(a.lease.id,'run');assert.equal(failed.released,false);assert.equal((await fs.readdir(path.join(r.root,'locks'))).length,2);assert.equal((await fs.readFile(failed.receipt.id,'utf8')).includes('private-down-token'),false);r.mutate('');assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);}finally{await r.cleanup();}
});
test('changed source fails before auth copy or allocation',async()=>{
 const r=await setup();try{await fs.writeFile(path.join(r.source,'tracked'),'changed');await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal));assert.equal(r.calls.some(c=>c.binary==='bash'||c.args.includes('up')),false);assert.deepEqual(await fs.readdir(path.join(r.root,'locks')),[]);}finally{await r.cleanup();}
});

test('distinct ModeCloud profile preserves supplemental overlay and named work-volume topology',async()=>{
 const r=await setup('legacy-real-codex-podman');try{
 const a=await r.lifecycle.acquire(r.request,new AbortController().signal);assert.equal(a.workspaceId,'E2E-WS');assert.equal(a.apiOrigin,a.filesOrigin);assert.equal(a.repo,'/work/source-repos/aft-repo');
 const o=JSON.parse(await fs.readFile(path.join(r.driver.runtimeRoot,'compose.json'),'utf8'));assert.equal(o.services['fleet-db'].environment.FLEET_AUTH_DEV_MODE,'true');assert.equal(o.services['fleet-db'].environment.FLEET_AUTHZ_ENABLED,'false');
 assert.equal(o.services['loom-serve'].environment.LOOM_DRIVER_TASK_RUNNER_CMD_JSON,null);assert.equal(o.services['loom-serve'].environment.FLUE_REPO,'/opt/flue');assert.ok(o.services['loom-serve'].volumes.every((v:string)=>v.endsWith(':ro')));
 assert.ok(o.volumes['loom-work']);assert.ok(r.calls.some(c=>c.args.includes('deploy/podman-stack/compose.yaml')));assert.equal(r.calls.some(c=>c.args.includes('test/local-mode/docker-compose.agents.yml')),false);
 const raw=JSON.stringify(a);assert.ok(r.driver.fixtureSecrets.every(secret=>!raw.includes(secret)));assert.equal((await r.lifecycle.observe(a.lease.id,'run')).services.length,6);assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});
for(const issue of ['hostbind','writable-auth','embedded','unhealthy','probe-leak','probe-incomplete'])test(`ModeCloud ${issue} cannot become an available fixture`,async()=>{
 const r=await setup('legacy-real-codex-podman');r.mutate(issue);try{await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal));}finally{await r.cleanup();}
});

test('owned container observations validate the same container generation after the fixed read',async()=>{
 const r=await setup('agents-emulator');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
 const override=JSON.parse(await fs.readFile(path.join(r.driver.runtimeRoot,'compose.json'),'utf8'));
 assert.equal(override.services['loom-local'].environment.AFT_FIXTURE_NAMESPACE,'owned-container');assert.equal(override.services['loom-local'].environment.AFT_FIXTURE_LEASE_ID,a.lease.id);
 const before=r.calls.filter(c=>c.args.includes('exec')).length;
 r.onExec(async()=>r.mutate('stale'));
 await assert.rejects(r.driver.nativeRead({operation:'agent-history',agentId:'agt_owned'},signal));
 assert.equal(r.calls.filter(c=>c.args.includes('exec')).length,before+1);
 r.mutate('');r.onExec(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});
test('container operation excludes teardown while a fixed read is in flight',async()=>{
 const r=await setup('agents-emulator');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
 let enter!:()=>void,leave!:()=>void;const entered=new Promise<void>(resolve=>{enter=resolve;}),gate=new Promise<void>(resolve=>{leave=resolve;});
 r.onExec(async()=>{enter();await gate;});const read=r.driver.nativeRead({operation:'registration'},signal);await entered;
 const failed=await r.lifecycle.release(a.lease.id,'run');assert.equal(failed.released,false);assert.equal(r.calls.some(c=>c.args.includes('down')),false);
 leave();await read;r.onExec(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});
test('foreign retained API generation and unbound model generation cannot dispatch container mutations',async()=>{
 const r=await setup('agents-emulator');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.calls.filter(c=>c.args.includes('exec')).length;
 await assert.rejects(r.driver.requestOwnedHttp('api','POST','/api/workspaces',{},signal,'foreign'));
 await assert.rejects(r.driver.requestOwnedHttp('fake-model','POST','/__script',{},signal,'unbound-model-generation'));
 assert.equal(r.calls.filter(c=>c.args.includes('exec')).length,before);assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('unregistered model generations deny every mutation including omitted generation and direct protocol calls',async()=>{
 const r=await setup('agents-emulator');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.calls.filter(c=>c.args.includes('exec')).length;
 for(const relative of ['/__script','/__reset','/__fixture'])for(const generation of [undefined,'foreign','generation']){
   await assert.rejects(r.driver.requestOwnedHttp('fake-model','POST',relative,{},signal,generation));
   await assert.rejects(r.driver.nativeRead({operation:'fixture-http',method:'POST',relativePath:relative,body:{}} as unknown as Parameters<typeof r.driver.nativeRead>[0],signal));
 }
 assert.equal(r.calls.filter(c=>c.args.includes('exec')).length,before);
 await r.driver.requestOwnedHttp('fake-model','GET','/__requests',null,signal);
 assert.equal(r.calls.filter(c=>c.args.includes('exec')).length,before+1);assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('actual container RUN_ID is read back under its lease and is distinct from the engine run identity',async()=>{
 for(const configured of [undefined,'af12345678']){
 const r=await setup('agents-emulator',configured);try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),fact=await r.driver.runtimeIdentity(signal);
 const override=JSON.parse(await fs.readFile(path.join(r.driver.runtimeRoot,'compose.json'),'utf8'));
 assert.equal(fact.fixtureRunId,override.services['loom-local'].environment.RUN_ID);assert.match(fact.fixtureRunId,/^af[a-z0-9]{8}$/);
 assert.notEqual(fact.fixtureRunId,a.lease.runId);if(configured)assert.equal(fact.fixtureRunId,configured);
 for(const issue of ['wrong-run-id','wrong-run-lease']){r.mutate(issue);await assert.rejects(r.driver.runtimeIdentity(signal));}
 r.mutate('');assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}}
});
test('invalid existing source RUN_ID fails before auth, allocation or commands',async()=>{
 const r=await setup('agents-emulator','engine-run-uuid');try{
 await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal));assert.equal(r.calls.length,0);
 assert.deepEqual(await fs.readdir(path.join(r.root,'locks')),[]);
 }finally{await r.cleanup();}
});
