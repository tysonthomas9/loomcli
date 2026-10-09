import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { ComposeFixtureDriver, type ProductionConfig, type ProcessRequest } from './production.js';
import { FixtureLifecycle, FixtureError, type FixturePlan } from './lifecycle.js';
const hash = (v: string | Uint8Array) => createHash('sha256').update(v).digest('hex');
async function setup(profile = 'agents-real-opencode') {
 const root = await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname), 'test-artifacts-'));
 const source = path.join(root,'source'), build = path.join(root,'build');
 for (const dir of [source,build,path.join(root,'home'),path.join(root,'locks')]) await fs.mkdir(dir);
 await fs.writeFile(path.join(source,'tracked'),'source');
 const digest = (es: {relativePath:string;sha256:string}[]) => hash([...es].sort((a,b)=>a.relativePath<b.relativePath?-1:1).map(e=>`${e.sha256}  ${e.relativePath}\n`).join(''));
 const se = [{relativePath:'tracked',sha256:hash('source')}];
 const image = 'sha256:'+'f'.repeat(64), receipt = JSON.stringify({imageId:image,sourceManifestSha256:digest(se)});
 await fs.writeFile(path.join(build,'container-image.json'),receipt);
 const elf=Buffer.from([0x7f,0x45,0x4c,0x46,1,2]); await fs.writeFile(path.join(build,'emulator'),elf);
 const be=[{relativePath:'container-image.json',sha256:hash(receipt)},{relativePath:'emulator',sha256:hash(elf)}];
 const revision={repository:'compose-test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:digest(se),buildManifestSha256:digest(be)};
 const registered={revision,source:{root:source,entries:se},build:{root:build,entries:be}};
 const connection={Identity:'/owned/key',Name:'owned',URI:'ssh://owned'};
 const config:ProductionConfig={loom:registered,fleet:registered,engine:registered,adapter:registered,tempParent:root,lockParent:path.join(root,'locks'),hostHome:path.join(root,'home'),toolPath:'/pinned/toolchain',connection:'owned',connectionFingerprint:hash(JSON.stringify([connection])),minimumFreeBytes:1,attestedImages:true,emulatorBinary:{path:path.join(build,'emulator'),sha256:hash(elf)}};
 const plan:FixturePlan={profile,loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,model:profile==='agents-emulator'?'aft/m':'openai/m',maxCases:10,caseCount:1,selectionSha256:'d'.repeat(64),leaseDurationMs:10000};
 const calls:ProcessRequest[]=[]; let project='',up=false,change='',port=5000,serial=0;
 const services=['redis','fleet-db','loom-local','ui-local'];
 const run=async (r:ProcessRequest)=>{
  calls.push(r); const a=r.args;
  if(r.binary==='git') return a[0]==='rev-parse'?(a[1]==='HEAD'?revision.commit:revision.tree):a[0]==='ls-files'?'tracked\0':'';
  project=r.env.LOCAL_MODE_COMPOSE_PROJECT||project;
  if(r.binary==='bash') return '';
  if(a[0]==='system') return JSON.stringify([{...connection,URI:change==='connection'?'ssh://foreign':connection.URI}]);
  if(a.includes('image')) return JSON.stringify([{Id:image}]);
  if(a.includes('compose')) { if(a.includes('up')) {up=true;if(change==='fail-up')throw new Error('Bearer private-up-token');} if(a.includes('down')) {if(change==='fail-down')throw new Error('secret=private-down-token');up=false;}return ''; }
  if(a.includes('ps')) return up?services.map(s=>`container-${s}`).join('\n'):'';
  if(a.includes('ls')) return up?(a.includes('volume')?'volume-owned':'network-owned'):'';
  if(a.includes('inspect')) {
   const id=a.at(-1)!,container=id.startsWith('container-'),service=id.replace('container-','');
   const labels={'com.docker.compose.project':project,'io.loom.aft.lease':change==='foreign'?'foreign':'opaque-fixture','com.docker.compose.service':service};
   const index={'fleet-db':0,'loom-local':1,'ui-local':2}[service as 'fleet-db'];
   return JSON.stringify([container?{Id:id,Image:change==='wrong-image'?'sha256:'+'e'.repeat(64):image,Config:{Labels:labels},State:{StartedAt:change==='stale'?'new':'generation',Pid:123,Status:'running'},NetworkSettings:{Ports:{'8080/tcp':[{HostPort:String(change==='wrong-port'?5999:5000+index)}]}}}:{Id:id,Name:id,Labels:labels,CreatedAt:'created'}]);
  }return '';
 };
 const files={...fs,statfs:async()=>({type:0,blocks:20*1024**3,bavail:20*1024**3,bfree:20*1024**3,bsize:1,files:1000,ffree:1000})} as unknown as typeof fs;
 const http=async (_origin:string,relative:string)=>relative.endsWith('/LOCALMODE')?{data:{id:'LOCALMODE',path:'/root/.loom/workspaces/LOCALMODE',repos:[{name:'source-repo',path:'/root/.loom/workspaces/LOCALMODE/source-repo'}]}}:relative.endsWith('/models')?{providers:[{models:[{id:plan.model},{id:'openai/alternate'}]}]}:{};
 const driver=new ComposeFixtureDriver(config,run,files,()=>`id${++serial}`,async()=>({port:port++,async release(){}}),http);
 const lifecycle=new FixtureLifecycle([plan],()=>driver,()=>1000,()=> 'opaque-fixture');
 const request={runId:'run',profile,loomRevision:revision,fleetRevision:revision,model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256};
 return {root,source,driver,lifecycle,request,calls,mutate(v:string){change=v;},async cleanup(){await fs.rm(root,{recursive:true});}};
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
