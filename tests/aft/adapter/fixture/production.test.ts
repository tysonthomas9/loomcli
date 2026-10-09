import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { ComposeFixtureDriver, type ProductionConfig, type ProcessRequest } from './production.js';
import { FixtureLifecycle, FixtureError, type FixturePlan } from './lifecycle.js';
import { materializeRenderer } from './renderer-fixtures.test.js';
const hash = (v: string | Uint8Array) => createHash('sha256').update(v).digest('hex');
async function setup(profile = 'agents-real-opencode',fixtureRunId?:string,useProductionReadiness=false,requestTime?:()=>number) {
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
 let onExec:((request:ProcessRequest)=>Promise<void>)|undefined,onRestart:(()=>Promise<void>)|undefined,onRead:((signal:AbortSignal)=>Promise<void>)|undefined;
 let restartMode='success',restarted=false,readFails=false,clockNow=0;const readinessStatuses:number[]=[],delays:number[]=[];
 const calls:ProcessRequest[]=[]; let project='',up=false,change='',port=5000,serial=0;
 const services=cloud?['redis','fleet-auth-seed','fleet-db','loom-serve','worker','stub-upstream']:['redis','fleet-db','loom-local','ui-local'];
 const run=async (r:ProcessRequest)=>{
  calls.push(r); const a=r.args;
  if(r.binary==='git') return a[0]==='rev-parse'?(a[1]==='HEAD'?revision.commit:revision.tree):a[0]==='ls-files'?se.map(e=>e.relativePath).join('\0')+'\0':'';
  project=r.env.LOCAL_MODE_COMPOSE_PROJECT||r.env.LOOM_STACK_PROJECT||project;
  if(r.binary==='bash') return '';
  if(a[0]==='system') return JSON.stringify([{...connection,URI:change==='connection'?'ssh://foreign':connection.URI}]);
  if(a.includes('image')) return JSON.stringify([{Id:image}]);
  if(a.includes('compose')) { if(a.includes('up')) {up=true;if(change==='fail-up')throw new Error('Bearer private-up-token');} if(a.includes('down')) {if(change==='fail-down')throw new Error('secret=private-down-token');up=false;}
   if(a.includes('restart')){await onRestart?.();if(restartMode!=='unchanged')restarted=true;if(restartMode==='throw-after')throw new Error('Bearer private-restart-token');}return ''; }
  if(a.includes('logs'))return change==='embedded'?'embedded fleet-db started':'opened cloud fleet-db client';
  if(a.includes('top'))return 'PID COMMAND\n123 loom\nBearer private-top-token\n';
  if(a.includes('exec')){await onExec?.(r);if(a.at(-1)?.includes('runtime-identity')){
    const override=JSON.parse(await fs.readFile(path.join(driver.runtimeRoot,'compose.json'),'utf8'));
    const service=override.services[cloud?'loom-serve':'loom-local'];return JSON.stringify({runId:change==='wrong-run-id'?'foreign-run':service.environment.RUN_ID,leaseId:change==='wrong-run-lease'?'foreign-lease':service.environment.AFT_FIXTURE_LEASE_ID});
  }return a.at(-1)?.includes('controlled-codex-preflight') ? JSON.stringify({ready:true,cleaned:change!=='probe-leak',complete:change!=='probe-incomplete'}) : JSON.stringify({sourceRepo:'/work/source-repos/aft-repo'});}
  if(a.includes('ps')) return up?services.flatMap(s=>{const replacement=restarted&&['new-id','double-target'].includes(restartMode)&&s==='loom-local';
    return replacement&&restartMode==='double-target'?[`container-${s}`,`container-${s}-replacement`]:[`container-${s}${replacement?(change==='target-only-id'?'-replacement-later':'-replacement'):''}`];}).join('\n'):'';
  if(a.includes('ls')) return up?(a.includes('volume')?'volume-owned':'network-owned'):'';
  if(a.includes('inspect')) {
   const id=a.at(-1)!,container=id.startsWith('container-'),service=id.replace('container-','').replace(/-replacement(?:-later)?$/,'');
   const labels={'com.docker.compose.project':project,'io.loom.aft.lease':change==='foreign'?'foreign':'opaque-fixture','com.docker.compose.service':service};
   const mappings:Record<string,number>=cloud?{'loom-serve':0,'fleet-db':1,'stub-upstream':2}:{'fleet-db':0,'loom-local':1,'ui-local':2};const index=mappings[service] ?? 0;
   const target=restarted&&service==='loom-local';
   const exited=change==='target-exit'&&service==='loom-local';
   return JSON.stringify([container?{Id:id,Image:change==='wrong-image'?'sha256:'+'e'.repeat(64):image,Config:{Labels:labels},State:{StartedAt:change==='target-only-stale'&&service==='loom-local'?'later-generation':change==='stale'?'new':target&&restartMode!=='same-start'?'restarted':'generation',Pid:exited?0:target&&restartMode!=='same-pid'||change==='pid-only'&&service==='loom-local'?456:123,Status:service==='fleet-auth-seed'||exited?'exited':'running',ExitCode:0,Health:{Status:change==='unhealthy'?'unhealthy':'healthy'}},Mounts:[{Destination:'/work',Type:change==='hostbind'?'bind':'volume',Name:change==='foreign-volume'?'foreign':'volume-owned',RW:true},{Destination:'/home/node/.codex',Type:'bind',Source:auth,RW:change==='writable-auth'},{Destination:'/opt/aft',Type:'bind',Source:change==='foreign-adapter'?'/foreign':config.adapter.build.root,RW:false},{Destination:'/opt/webui',Type:'bind',Source:frontend,RW:false},{Destination:'/srv',Type:'bind',Source:frontend,RW:false}],NetworkSettings:{Networks:{owned:{NetworkID:change==='foreign-network'?'network-foreign':'network-owned'}},Ports:{'8080/tcp':[{HostPort:String(change==='wrong-port'?5999:5000+index)}]}}}:{Id:id,Name:id,Labels:labels,CreatedAt:'created'}]);
  }return '';
 };
 const files={...fs,statfs:async()=>({type:0,blocks:20*1024**3,bavail:20*1024**3,bfree:20*1024**3,bsize:1,files:1000,ffree:1000})} as unknown as typeof fs;
 const http=async (_origin:string,relative:string,signal:AbortSignal)=>{if(restarted&&relative==='/api/config'){await onRead?.(signal);if(readFails)throw Error('private API failure');}
  return relative.endsWith('/repos')?{success:true,repos:[{name:'aft-repo'}]}:relative.endsWith('/LOCALMODE')?{data:{id:'LOCALMODE',path:'/root/.loom/workspaces/LOCALMODE',repos:[{name:'source-repo',path:'/root/.loom/workspaces/LOCALMODE/source-repo'}]}}:relative.endsWith('/models')?{providers:[{models:[{id:plan.model},{id:'openai/alternate'}]}]}:{};};
 const driver=new ComposeFixtureDriver(config,run,files,()=>`id${++serial}`,async()=>({port:port++,async release(){}}),http,async()=>({status:201,body:{}}),
  useProductionReadiness?undefined:async(origin,signal)=>{await http(origin,'/api/config',signal);return {status:readinessStatuses.shift()??200,complete:true};},
  {now:()=>clockNow,monotonicNow:requestTime??(()=>clockNow),async nextAttempt(signal){signal.throwIfAborted();delays.push(1000);clockNow+=1000;}});
 const lifecycle=new FixtureLifecycle([plan],()=>driver,()=>1000,()=> 'opaque-fixture');
 const request={runId:'run',profile,loomRevision:revision,fleetRevision:revision,model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256};
 return {root,source,driver,lifecycle,request,calls,onExec(callback:(request:ProcessRequest)=>Promise<void>){onExec=callback;},mutate(v:string){change=v;},
  restartMode(value:string){restartMode=value;},restoreOriginalContainer(){restarted=false;restartMode='success';},onRestart(callback:()=>Promise<void>){onRestart=callback;},onRead(callback:(signal:AbortSignal)=>Promise<void>){onRead=callback;},failRead(){readFails=true;},statuses(...values:number[]){readinessStatuses.push(...values);},delays,
  advance(ms:number){clockNow+=ms;},async cleanup(){await fs.rm(root,{recursive:true});}};
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

test('Compose private artifact bridge accepts only this lifecycle writer and retains exact bytes after resource teardown',async()=>{
 const r=await setup('agents-real-opencode');try{
  const acquired=await r.lifecycle.acquire(r.request,new AbortController().signal);
  const actual=JSON.parse(await r.driver.readIssuedArtifact(acquired.ownershipArtifact,new AbortController().signal));
  assert.equal(actual.kind,'acquire');assert.equal(actual.value.leaseId,acquired.lease.id);
  await assert.rejects(r.driver.readIssuedArtifact({...acquired.ownershipArtifact,bytes:1},new AbortController().signal));
  const released=await r.lifecycle.release(acquired.lease.id,'run');assert.equal(released.released,true);
  assert.ok(await fs.readFile(released.receipt.id));
  await assert.rejects(r.driver.readIssuedArtifact(acquired.ownershipArtifact,new AbortController().signal));
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

test('selected SSE actor restarts only owned loom-local and observes changed PID plus start time and readiness',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const before=(await r.lifecycle.observe(a.lease.id,'run')).services.find(value=>value.id==='container-loom-local')!;
  const waitsBefore=r.calls.filter(call=>call.args.includes('wait')).length;
  const fact=await r.driver.restartOwnedServe(before.id,before.generation,signal);
  assert.deepEqual(fact.before,{containerId:before.id,initPid:123,startedAt:'generation',generation:before.generation});
  assert.deepEqual(fact.after,{containerId:before.id,initPid:456,startedAt:'restarted',generation:'container-loom-local:restarted'});
  assert.deepEqual(fact.readiness,{path:'/api/config',status:200,complete:true,attempts:1,elapsedMs:0,windowMs:180000,requestTimeoutMs:3000});assert.equal(fact.scope,'loom-local-plus-OpenCode');
  const restart=r.calls.filter(call=>call.args.includes('restart'));assert.equal(restart.length,1);assert.deepEqual(restart[0]!.args.slice(-2),['restart','loom-local']);
  assert.equal(r.calls.filter(call=>call.args.includes('wait')).length,waitsBefore);
  const retained=JSON.parse(await fs.readFile(fact.receipt.id,'utf8'));assert.equal(retained.value.fact.after.initPid,456);
  assert.equal(JSON.stringify(retained).includes('private-top-token'),false);assert.deepEqual(retained.value.processListing.redaction.replacedTextPaths,['']);
  assert.equal(r.calls.filter(call=>call.args.includes('top')).length,2);
  await assert.rejects(r.driver.restartOwnedServe(before.id,before.generation,signal));assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.observe(a.lease.id,'run')).services.find(value=>value.id===before.id)?.generation,fact.after.generation);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

for(const mismatch of ['foreign-id','foreign-generation','foreign-label','emulator','cloud'])test(`selected SSE ${mismatch} denies the restart before effect`,async()=>{
 const r=await setup(mismatch==='emulator'?'agents-emulator':mismatch==='cloud'?'legacy-real-codex-podman':'agents-real-opencode');try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  if(mismatch==='foreign-label')r.mutate('foreign');
  await assert.rejects(r.driver.restartOwnedServe(mismatch==='foreign-id'?'foreign':'container-loom-local',mismatch==='foreign-generation'?'foreign':'container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,0);
  r.mutate('');assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

for(const failure of ['unchanged','same-pid','same-start','throw-after','read'])test(`selected SSE ${failure} cannot report restart success and retains exact cleanup authority`,async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  if(failure==='read')r.failRead();else r.restartMode(failure);
  let receipt='';await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal),error=>{
   assert.ok(error instanceof FixtureError);assert.ok(error.receipt);receipt=error.receipt.id;return true;
  });
  const bytes=await fs.readFile(receipt,'utf8');assert.equal(bytes.includes('private-restart-token'),false);assert.equal(bytes.includes('private API failure'),false);
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  const released=await r.lifecycle.release(a.lease.id,'run');
  assert.equal(released.released,failure!=='throw-after');
  if(failure==='throw-after'){assert.equal(r.calls.some(call=>call.args.includes('down')),false);r.restoreOriginalContainer();assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);}
 }finally{await r.cleanup();}
});

test('selected SSE operation excludes native reads and teardown until restart readiness settles',async()=>{
 const r=await setup();let enter!:()=>void,leave!:()=>void;let pending:ReturnType<ComposeFixtureDriver['restartOwnedServe']>|undefined;
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const entered=new Promise<void>(resolve=>{enter=resolve;}),gate=new Promise<void>(resolve=>{leave=resolve;});
  r.onRead(async()=>{enter();await gate;});pending=r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);await entered;
  const execs=r.calls.filter(call=>call.args.includes('exec')).length;
  await assert.rejects(r.driver.nativeRead({operation:'registration'},signal));
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:restarted',signal));
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);
  assert.equal(r.calls.filter(call=>call.args.includes('exec')).length,execs);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  leave();assert.equal((await pending).after.initPid,456);r.onRead(async()=>{});
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{leave?.();await pending?.catch(()=>{});await r.cleanup();}
});

test('replacement after selected SSE readiness denies observation instead of adopting a second generation',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  r.onRead(async()=>r.mutate('stale'));
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.mutate('');r.onRead(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('selected SSE rejects changed source before restart and retains cleanup after caller abort during dispatch',async()=>{
 const changed=await setup();try{
  const signal=new AbortController().signal,a=await changed.lifecycle.acquire(changed.request,signal);
  await fs.writeFile(path.join(changed.source,'tracked'),'replacement');
  await assert.rejects(changed.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(changed.calls.filter(call=>call.args.includes('restart')).length,0);
  await fs.writeFile(path.join(changed.source,'tracked'),'source');assert.equal((await changed.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await changed.cleanup();}
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),abort=new AbortController();
  r.onRestart(async()=>abort.abort());
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',abort.signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.restoreOriginalContainer();assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('container PID replacement without start identity cannot be rebound, while terminal owned container cleanup stays available',async()=>{
 for(const change of ['pid-only','target-exit']){const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.mutate(change);
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,0);
  if(change==='pid-only'){
   await assert.rejects(r.lifecycle.observe(a.lease.id,'run'));assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);
   assert.equal(r.calls.some(call=>call.args.includes('down')),false);r.mutate('');
  }
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}}
});

test('parent review source readiness clock starts only after restart completes',async()=>{
 const r=await setup(); const original=AbortSignal.timeout;let duringRestart=-1;const seen:number[]=[];
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const before=(await r.lifecycle.observe(a.lease.id,'run')).services.find(value=>value.id==='container-loom-local')!;
  AbortSignal.timeout=((ms:number)=>{seen.push(ms);return original.call(AbortSignal,ms);}) as typeof AbortSignal.timeout;
  r.onRestart(async()=>{duringRestart=seen.filter(ms=>ms===180000).length;});
  await r.driver.restartOwnedServe(before.id,before.generation,signal);
  assert.equal(duringRestart,0,'readiness window must not start before restart finishes');
  assert.equal(seen.filter(ms=>ms===180000).length,1);
 }finally{AbortSignal.timeout=original;await r.cleanup();}
});

test('selected SSE request has its own three-second bound and cannot report success after that bound expires',async()=>{
 const r=await setup(),original=AbortSignal.timeout,requestTimeout=new AbortController();const seen:number[]=[];
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  AbortSignal.timeout=((ms:number)=>{seen.push(ms);return ms===3000?requestTimeout.signal:original.call(AbortSignal,ms);}) as typeof AbortSignal.timeout;
  r.onRead(async requestSignal=>{assert.equal(requestSignal.aborted,false);requestTimeout.abort();assert.equal(requestSignal.aborted,true);});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.deepEqual(seen.slice(0,3),[60000,180000,3000]);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{AbortSignal.timeout=original;await r.cleanup();}
});

test('selected SSE dispatch expiration cannot start a readiness window or retry the mutation',async()=>{
 const r=await setup(),original=AbortSignal.timeout,dispatchTimeout=new AbortController();const seen:number[]=[];
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  AbortSignal.timeout=((ms:number)=>{seen.push(ms);return ms===60000?dispatchTimeout.signal:original.call(AbortSignal,ms);}) as typeof AbortSignal.timeout;
  r.onRestart(async()=>dispatchTimeout.abort());
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(seen.includes(180000),false);assert.equal(seen.includes(3000),false);
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.restoreOriginalContainer();assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{AbortSignal.timeout=original;await r.cleanup();}
});

test('selected SSE accepts an authentic sole owned new-ID replacement and retains predecessor evidence',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(fact.before.containerId,'container-loom-local');assert.equal(fact.after.containerId,'container-loom-local-replacement');
  const receipt=JSON.parse(await fs.readFile(fact.receipt.id,'utf8'));
  assert.equal(receipt.value.successor.containerIdChanged,true);assert.equal(receipt.value.successor.predecessorContainerId,fact.before.containerId);
  assert.match(receipt.value.successor.namespaceSha256,/^[a-f0-9]{64}$/);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.observe(a.lease.id,'run')).services.find(value=>value.id===fact.after.containerId)?.generation,fact.after.generation);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('selected SSE retries failed fixed config reads inside the original window without requiring container health',async()=>{
 const r=await setup();let reads=0;
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.mutate('unhealthy');
  const waitsBefore=r.calls.filter(call=>call.args.includes('wait')).length;
  r.onRead(async()=>{if(++reads===1)throw Error('private transient config read');});r.statuses(503,200);
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,3);assert.equal(fact.readiness.attempts,3);assert.equal(fact.readiness.elapsedMs,2000);assert.equal(fact.readiness.status,200);
  assert.deepEqual(r.delays,[1000,1000]);assert.equal(r.calls.filter(call=>call.args.includes('wait')).length,waitsBefore);
  const receipt=await fs.readFile(fact.receipt.id,'utf8');assert.equal(receipt.includes('private transient'),false);
  assert.deepEqual(JSON.parse(receipt).value.readinessAttempts.map((value:{status?:number;complete:boolean})=>({...(value.status===undefined?{}:{status:value.status}),complete:value.complete})),
   [{complete:false},{status:503,complete:true},{status:200,complete:true}]);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('source failure-only deadline accepts a bounded successful response crossing the deadline',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  let dispatched=false,advanced=false;r.onRestart(async()=>{dispatched=true;});
  r.onExec(async request=>{if(dispatched&&!advanced&&request.args.at(-1)?.includes('runtime-identity')){advanced=true;r.advance(179000);}});
  r.onRead(async()=>r.advance(2000));
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(fact.readiness.status,200);assert.equal(fact.readiness.elapsedMs,181000);assert.equal(fact.readiness.attempts,1);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);assert.deepEqual(r.delays,[]);
  const receipt=JSON.parse(await fs.readFile(fact.receipt.id,'utf8'));
  assert.deepEqual(receipt.value.readinessClock,{policy:'deadline-after-failed-request',invocationStartMs:0,readinessStartMs:0,failureDeadlineSeconds:180});
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('source final retry sleep can cross the failure deadline before a successful request',async()=>{
 const r=await setup();let reads=0;try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  let dispatched=false,advanced=false;r.onRestart(async()=>{dispatched=true;});
  r.onExec(async request=>{if(dispatched&&!advanced&&request.args.at(-1)?.includes('runtime-identity')){advanced=true;r.advance(179000);}});
  r.statuses(503,200);r.onRead(async()=>{reads++;});
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,2);assert.equal(fact.readiness.status,200);assert.equal(fact.readiness.elapsedMs,180000);
  assert.deepEqual(r.delays,[1000]);assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('source failure checks integer invocation seconds and cannot retry a failed request at the deadline',async()=>{
 const r=await setup();let reads=0;try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  let dispatched=false,advanced=false;r.onRestart(async()=>{r.advance(500);dispatched=true;});
  r.onExec(async request=>{if(dispatched&&!advanced&&request.args.at(-1)?.includes('runtime-identity')){advanced=true;r.advance(178000);}});
  r.statuses(503);r.onRead(async()=>{reads++;r.advance(1500);});
  let receipt='';await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal),error=>{
   receipt=(error as FixtureError).receipt!.id;return error instanceof FixtureError;
  });
  const data=JSON.parse(await fs.readFile(receipt,'utf8')).value;
  assert.equal(data.readinessAttempts[0].elapsedMs,179500);assert.equal(data.readinessAttempts[0].status,503);
  assert.equal(reads,1);assert.deepEqual(r.delays,[]);assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('failure-only window expiration never vetoes success but external abort still does',async()=>{
 for(const externalAbort of [false,true]){
  const r=await setup(),original=AbortSignal.timeout,deadline=new AbortController(),signal=new AbortController();
  try{
   const a=await r.lifecycle.acquire(r.request,signal.signal);
   AbortSignal.timeout=((ms:number)=>ms===180000?deadline.signal:original.call(AbortSignal,ms)) as typeof AbortSignal.timeout;
   r.onRead(async()=>{deadline.abort();if(externalAbort)signal.abort();});
   if(externalAbort)await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal.signal));
   else assert.equal((await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal.signal)).readiness.status,200);
   assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
   assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
  }finally{AbortSignal.timeout=original;await r.cleanup();}
 }
});

test('source late success allowance never waives the individual three-second request bound',async()=>{
 const r=await setup();let reads=0;try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  r.onRead(async()=>{if(++reads===1)r.advance(3001);});
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,2);assert.equal(fact.readiness.attempts,2);assert.equal(fact.readiness.elapsedMs,4001);assert.deepEqual(r.delays,[1000]);
  const receipt=JSON.parse(await fs.readFile(fact.receipt.id,'utf8'));
  assert.equal(receipt.value.readinessAttempts[0].complete,false);assert.equal(receipt.value.readinessAttempts[0].status,undefined);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('source integer clock preserves the invocation subsecond phase instead of flooring elapsed milliseconds',async()=>{
 const r=await setup();let reads=0;try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.advance(500);
  let dispatched=false,advanced=false;r.onRestart(async()=>{r.advance(500);dispatched=true;});
  r.onExec(async request=>{if(dispatched&&!advanced&&request.args.at(-1)?.includes('runtime-identity')){advanced=true;r.advance(179000);}});
  r.statuses(503,200);r.onRead(async()=>{if(++reads===1)r.advance(500);});
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,2);assert.equal(fact.readiness.elapsedMs,180500);assert.deepEqual(r.delays,[1000]);
  const receipt=JSON.parse(await fs.readFile(fact.receipt.id,'utf8'));
  assert.deepEqual(receipt.value.readinessClock,{policy:'deadline-after-failed-request',invocationStartMs:500,readinessStartMs:1000,failureDeadlineSeconds:181});
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('individual request duration is independent of the source wall clock deadline',async()=>{
 let requestTime=0;const r=await setup('agents-real-opencode',undefined,false,()=>requestTime);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  r.onRead(async()=>{r.advance(184000);requestTime=100;});
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(fact.readiness.status,200);assert.equal(fact.readiness.elapsedMs,184000);assert.deepEqual(r.delays,[]);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

for(const change of ['foreign','foreign-volume','foreign-network','foreign-adapter','wrong-run-id','wrong-run-lease','stale','double-target'])test(`selected SSE successor ${change} cannot acquire cleanup or observation authority`,async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  r.onRestart(async()=>{if(change==='double-target')r.restartMode('double-target');else r.mutate(change);});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.mutate('');r.restoreOriginalContainer();assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('successor replacement during its runtime marker read is denied before the new generation is enrolled',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  r.onExec(async request=>{if(request.args.includes('container-loom-local-replacement'))r.mutate('stale');});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.mutate('');r.restoreOriginalContainer();r.onExec(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('production readiness consumes actual response bytes without inventing a JSON or redirect-follow predicate',async()=>{
 const r=await setup('agents-real-opencode',undefined,true),original=globalThis.fetch;let reads=0;
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  globalThis.fetch=(async(url,options)=>{
   assert.equal(String(url),'http://127.0.0.1:5001/api/config');assert.equal(options?.redirect,'manual');
   assert.ok(options?.signal);reads++;return new Response('actual non-JSON config bytes',{status:reads===1?503:302});
  }) as typeof fetch;
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,2);assert.equal(fact.readiness.status,302);assert.equal(fact.readiness.elapsedMs,1000);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{globalThis.fetch=original;await r.cleanup();}
});

test('production readiness stream failure cannot turn a partial body into successful config readiness',async()=>{
 const r=await setup('agents-real-opencode',undefined,true),original=globalThis.fetch;let reads=0;
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  globalThis.fetch=(async()=>++reads===1?new Response(new ReadableStream({start(controller){controller.error(Error('private body failure'));}}),{status:200}):
   new Response('actual complete body',{status:200})) as typeof fetch;
  const fact=await r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal);
  assert.equal(reads,2);assert.equal(fact.readiness.attempts,2);
  const receipt=await fs.readFile(fact.receipt.id,'utf8');assert.equal(receipt.includes('private body failure'),false);
  assert.equal(JSON.parse(receipt).value.readinessAttempts[0].complete,false);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{globalThis.fetch=original;await r.cleanup();}
});

test('selected SSE cannot attest a successful restart after source bytes change during readiness',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  r.onRead(async()=>fs.writeFile(path.join(r.source,'tracked'),'foreign source'));
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
 }finally{await r.cleanup();}
});

test('parent review failed first successor attestation cannot enroll a later target generation for cleanup',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  let changed=false;
  r.onExec(async request=>{if(!changed&&request.args.includes('container-loom-local-replacement')){changed=true;r.mutate('target-only-stale');}});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  const result=await r.lifecycle.release(a.lease.id,'run');
  assert.equal(result.released,false,'later target generation was not the captured successor');
  assert.equal(r.calls.some(call=>call.args.includes('down')),false);
 }finally{await r.cleanup();}
});

test('failed first successor marker cannot enroll a different target ID on cleanup',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  let changed=false;r.onExec(async request=>{if(!changed&&request.args.includes('container-loom-local-replacement')){changed=true;r.mutate('target-only-id');}});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  const failed=await r.lifecycle.release(a.lease.id,'run');assert.equal(failed.released,false);
  assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  assert.equal(r.calls.some(call=>call.args.includes('exec')&&call.args.includes('container-loom-local-replacement-later')),false);
  r.mutate('');r.onExec(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
 }finally{await r.cleanup();}
});
test('failed successor marker retains only its captured identity for read-only cleanup retries',async()=>{
 const r=await setup();try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  r.onExec(async request=>{if(request.args.includes('container-loom-local-replacement'))throw Error('Bearer private-marker-read');});
  let receipt='';await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal),error=>{
   assert.ok(error instanceof FixtureError);receipt=error.receipt!.id;return true;
  });
  const artifact=await fs.readFile(receipt,'utf8');assert.equal(artifact.includes('private-marker-read'),false);
  assert.ok(artifact.includes('attestationCandidate'));assert.ok(artifact.includes('container-loom-local-replacement'));
  assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,false);assert.equal(r.calls.some(call=>call.args.includes('down')),false);
  r.onExec(async()=>{});assert.equal((await r.lifecycle.release(a.lease.id,'run')).released,true);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
 }finally{await r.cleanup();}
});
test('pending successor cleanup attestation excludes other container operations until it settles',async()=>{
 const r=await setup();let enter!:()=>void,leave!:()=>void,pending:ReturnType<FixtureLifecycle['release']>|undefined;
 try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);r.restartMode('new-id');
  r.onExec(async request=>{if(request.args.includes('container-loom-local-replacement'))throw Error('unavailable marker');});
  await assert.rejects(r.driver.restartOwnedServe('container-loom-local','container-loom-local:generation',signal));
  const entered=new Promise<void>(resolve=>{enter=resolve;}),blocked=new Promise<void>(resolve=>{leave=resolve;});
  r.onExec(async request=>{if(request.args.includes('container-loom-local-replacement')){enter();await blocked;}});
  pending=r.lifecycle.release(a.lease.id,'run');await entered;const calls=r.calls.length;
  await assert.rejects(r.driver.nativeRead({operation:'runtime-identity'},signal));
  await assert.rejects(r.driver.prepareCleanup(signal));await assert.rejects(r.lifecycle.release(a.lease.id,'run'));
  assert.equal(r.calls.length,calls);leave();assert.equal((await pending).released,true);
  assert.equal(r.calls.filter(call=>call.args.includes('restart')).length,1);
 }finally{leave?.();await pending?.catch(()=>{});await r.cleanup();}
});
