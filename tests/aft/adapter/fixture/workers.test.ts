import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { RegisteredBuiltinWorkers } from './workers.js';
import { OwnedDescendants, type RegisteredIdentity, type RegisteredProcessHandle } from './descendants.js';
import type { Resource } from './lifecycle.js';

async function setup(exitObservation=false){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-workers-'));
 const config=path.join(root,'config'),cwd=path.join(root,'project'),stateRoot=path.join(cwd,'custom-state');
 await fs.mkdir(path.join(config,'workspaces','E2E-WS'),{recursive:true});await fs.mkdir(stateRoot,{recursive:true});
 const stateFile=path.join(stateRoot,'daemon-agents.json'),sidecarFile=path.join(config,'workspaces','E2E-WS','daemon.pid');
 const exe=path.join(root,'loom'),worktree=path.join(root,'managed','nova');
 const now='2026-10-09T01:02:03Z';
 const sidecar={pid:40,started_at:now,cwd,socket:path.join(stateRoot,'daemon.sock')};
 const row={worktree:'nova',role:'task',pid:41,status:'running',worktree_path:worktree,current_backend:'codex',
  epic_id:'epic-own',last_start:now,ownership_lease_id:'private-lease',ownership_fencing_token:9};
 const state={pid:40,started_at:now,agents:[row]};
 const write=async()=>{await fs.writeFile(sidecarFile,JSON.stringify(sidecar));await fs.writeFile(stateFile,JSON.stringify(state));};await write();
 const argv=[exe,'task',worktree,'--auto','--daemon-mode','--backend','codex','--parent','epic-own'];
 const identity:RegisteredIdentity={pid:41,generation:'kernel-worker-1',executable:exe,
  argvSha256:createHash('sha256').update(Buffer.from(argv.join('\0')+'\0')).digest('hex'),parentPid:40,configurationRoot:config,state:'running'};
 const parent={id:'owned-daemon',identity:{...identity,pid:40,generation:'kernel-daemon-1'}};
 let current={...identity},parentGeneration=parent.identity.generation,captures=0,abandons=0,stops=0,count=0,apiStops=0;
 let onCapture:(()=>Promise<void>)|undefined,onActor:(()=>Promise<void>)|undefined;
 let onInspect:((value:RegisteredIdentity)=>RegisteredIdentity)|undefined;
 const resources:Resource[]=[];const handles:RegisteredProcessHandle[]=[];
 const descendants=new OwnedDescendants({async capture(pid){captures++;assert.equal(resources.at(-1)?.generation,`unverified:${pid}`);
  await onCapture?.();const original={...current};let exited=false;
  const handle={identity:original,async inspect(){const value={...original,state:exited?'exited' as const:'running' as const};return onInspect?.(value)??value;},
   async stop(){stops++;exited=true;},async abandon(){abandons++;},
   ...(exitObservation?{async awaitExit(){exited=true;}}:{})};handles.push(handle);return handle;
 }},resource=>resources.push(resource));
 const stamp=async(filename:string)=>{const s=await fs.lstat(filename);return {path:filename,device:s.dev,inode:s.ino};};
 const registry=new RegisteredBuiltinWorkers({configurationRoot:await stamp(config),runtimeRoot:await stamp(root),
  workspaceId:'E2E-WS',daemonCwd:cwd,loomExecutable:exe},{
  async parent(){return parent;},async verifyParent(p){assert.equal(p.identity.generation,parentGeneration);},
  async verifyActor(name,actual){assert.equal(name,'nova');assert.equal(actual,worktree);await onActor?.();},nextId:()=>String(++count),
  async stop(fact,generation){assert.equal(fact.agentId,'nova');assert.equal(fact.workspaceId,'E2E-WS');assert.equal(generation,'owned-serve');apiStops++;return {status:202,body:{success:true}};}
 },descendants);
 return {root,config,stateRoot,stateFile,sidecarFile,sidecar,row,state,identity,parent,registry,descendants,resources,handles,write,
  setIdentity:(value:Partial<RegisteredIdentity>)=>{current={...current,...value};},
  replaceParent:()=>{parentGeneration='foreign-parent';},onCapture:(value:()=>Promise<void>)=>{onCapture=value;},
  onActor:(value:()=>Promise<void>)=>{onActor=value;},onInspect:(value:typeof onInspect)=>{onInspect=value;},
  apiStops:()=>apiStops,counts:()=>({captures,abandons,stops}),remove:()=>fs.rm(root,{recursive:true,force:true})};
}
const signal=()=>new AbortController().signal;

test('captures actual builtin registration under a retained owned parent without private lease data',async()=>{
 const s=await setup();try{
  const facts=await s.registry.refresh(signal());assert.equal(facts.length,1);assert.equal(facts[0]?.agentId,'nova');
  assert.equal(facts[0]?.generation,'kernel-worker-1');assert.equal(facts[0]?.kind,'worker');assert.equal(s.counts().captures,1);
  assert.ok(!JSON.stringify(facts).includes('private-lease'));assert.ok(!JSON.stringify(facts).includes('ownership_fencing'));
  assert.deepEqual(await s.registry.refresh(signal()),facts);assert.equal(s.counts().captures,1);
  await s.descendants.stop(facts[0]!.id,facts[0]!.generation);assert.equal(s.counts().stops,1);
 }finally{await s.remove();}
});

test('refuses foreign executable, argv, configuration and parent before granting a worker handle',async()=>{
 for(const mutation of [{executable:'/foreign/loom'},{argvSha256:'a'.repeat(64)},{configurationRoot:'/foreign/config'},{parentPid:50}]){
  const s=await setup();try{s.setIdentity(mutation);await assert.rejects(s.registry.refresh(signal()));
   assert.deepEqual(s.counts(),{captures:1,abandons:1,stops:0});assert.equal(s.resources[0]?.generation,'unverified:41');
  }finally{await s.remove();}
 }
});

test('retains captured resource when parent changes during capture; cleanup uses the exact generation',async()=>{
 const s=await setup();try{
  s.onCapture(async()=>s.replaceParent());await assert.rejects(s.registry.refresh(signal()));
  const enrolled=s.resources.at(-1)!;assert.equal(enrolled.generation,'kernel-worker-1');
  await s.descendants.stop(enrolled.id,enrolled.generation);assert.equal(s.counts().stops,1);
 }finally{await s.remove();}
});

test('refuses altered or ambiguous registration and custom roles without claiming collection absence',async()=>{
 for(const mutate of [
  (s:Awaited<ReturnType<typeof setup>>)=>{s.sidecar.pid=99;},
  (s:Awaited<ReturnType<typeof setup>>)=>{s.sidecar.socket='/foreign/daemon.sock';},
  (s:Awaited<ReturnType<typeof setup>>)=>{s.state.agents.push({...s.row});},
  (s:Awaited<ReturnType<typeof setup>>)=>{s.row.role='custom-lead';},
  (s:Awaited<ReturnType<typeof setup>>)=>{s.row.status='starting';}
 ]){const s=await setup();try{mutate(s);await s.write();await assert.rejects(s.registry.refresh(signal()));assert.equal(s.counts().captures,0);
 }finally{await s.remove();}}
});

test('registration changes during capture retain diagnostics and deny changed intent on retry',async()=>{
 const s=await setup();try{
  s.onCapture(async()=>{s.row.pid=42;await s.write();});await assert.rejects(s.registry.refresh(signal()));
  assert.equal(s.counts().captures,1);await assert.rejects(s.registry.refresh(signal()));assert.equal(s.counts().captures,1);
  const retained=s.resources.at(-1)!;await s.descendants.stop(retained.id,retained.generation);assert.equal(s.counts().stops,1);
 }finally{await s.remove();}
});

test('rejects inaccessible or symlinked state before capture and does not report an empty collection',async()=>{
 const s=await setup();try{
  await fs.rename(s.stateFile,s.stateFile+'-retained');await fs.symlink(s.stateFile+'-retained',s.stateFile);
  await assert.rejects(s.registry.refresh(signal()));assert.equal(s.counts().captures,0);
  await fs.unlink(s.stateFile);await assert.rejects(s.registry.refresh(signal()));assert.equal(s.counts().captures,0);
 }finally{await s.remove();}
});

test('unchanged registration cannot adopt a reused process generation after predecessor exit',async()=>{
 const s=await setup();try{
  const first=(await s.registry.refresh(signal()))[0]!;await s.descendants.stop(first.id,first.generation);
  s.setIdentity({generation:'foreign-reused-generation'});await assert.rejects(s.registry.refresh(signal()));
  assert.equal(s.counts().captures,1);
 }finally{await s.remove();}
});

test('serializes discovery before the first awaited actor check',async()=>{
 const s=await setup();let release!:()=>void,entered!:()=>void;try{
  const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  let calls=0;s.onActor(async()=>{if(++calls===1){entered();await blocked;}});
  const first=s.registry.refresh(signal());await ready;await assert.rejects(s.registry.refresh(signal()));release();
  assert.equal((await first).length,1);assert.equal(s.counts().captures,1);
 }finally{release?.();await s.remove();}
});

test('retains predecessor and enrolls a source-registered successor only after observed exit',async()=>{
 const s=await setup();try{
  const first=(await s.registry.refresh(signal()))[0]!;
  s.row.pid=42;s.row.last_start='2026-10-09T01:03:04Z';await s.write();
  await assert.rejects(s.registry.refresh(signal()));assert.equal(s.counts().captures,1);
  await s.descendants.stop(first.id,first.generation);s.setIdentity({pid:42,generation:'kernel-worker-2'});
  const second=(await s.registry.refresh(signal()))[0]!;assert.notEqual(second.id,first.id);
  assert.equal(second.generation,'kernel-worker-2');assert.equal(s.descendants.initial(first.id).generation,first.generation);
  await s.descendants.stop(second.id,second.generation);assert.equal(s.counts().stops,2);
 }finally{await s.remove();}
});

test('product worker stop requires an available exact exit observer before any API effect',async()=>{
 const s=await setup();try{
  const fact=(await s.registry.refresh(signal()))[0]!;
  await assert.rejects(s.registry.stop(fact.id,fact.generation,'owned-serve',signal()),/unsupported-capability/);
  assert.equal(s.apiStops(),0);assert.equal(s.counts().stops,0);
 }finally{await s.remove();}
});

test('product worker stop retains the API outcome and observed exit without invoking cleanup',async()=>{
 const s=await setup(true);try{
  const fact=(await s.registry.refresh(signal()))[0]!;
  const result=await s.registry.stop(fact.id,fact.generation,'owned-serve',signal());
  assert.equal(result.response.status,202);assert.equal(result.transition.afterGeneration,null);
  assert.equal((await s.descendants.inspect(fact.id,fact.generation)).state,'exited');
  assert.equal(s.apiStops(),1);assert.equal(s.counts().stops,0);
 }finally{await s.remove();}
});

test('retained reads require an already captured exact worker generation and do not discover one',async()=>{
 const s=await setup();try{
  await assert.rejects(s.registry.readRetained('registered-worker-1','kernel-worker-1',signal()));
  assert.equal(s.counts().captures,0);
  const fact=(await s.registry.refresh(signal()))[0]!;
  await assert.rejects(s.registry.readRetained(fact.id,'foreign-generation',signal()));
  const read=await s.registry.readRetained(fact.id,fact.generation,signal());
  assert.deepEqual(read.fact,fact);assert.equal(read.before.pid,41);assert.equal(read.after.state,'running');
  assert.ok(Object.isFrozen(read)&&Object.isFrozen(read.fact)&&Object.isFrozen(read.before)&&Object.isFrozen(read.after));
  assert.ok(!JSON.stringify(read).includes('private-lease'));assert.equal(s.counts().captures,1);assert.equal(s.apiStops(),0);
 }finally{await s.remove();}
});

test('retained predecessor reads survive a separately enrolled successor and inaccessible sidecars',async()=>{
 const s=await setup(true);try{
  const first=(await s.registry.refresh(signal()))[0]!;
  await s.registry.stop(first.id,first.generation,'owned-serve',signal());
  s.row.pid=42;s.row.last_start='2026-10-09T01:03:04Z';await s.write();s.setIdentity({pid:42,generation:'kernel-worker-2'});
  const second=(await s.registry.refresh(signal()))[0]!;
  await fs.unlink(s.stateFile);await fs.unlink(s.sidecarFile);
  const prior=await s.registry.readRetained(first.id,first.generation,signal());
  const next=await s.registry.readRetained(second.id,second.generation,signal());
  assert.deepEqual(prior.fact,first);assert.equal(prior.before.pid,41);assert.equal(prior.after.state,'exited');
  assert.deepEqual(next.fact,second);assert.equal(next.before.pid,42);assert.equal(next.after.state,'running');
  assert.equal(s.counts().captures,2);assert.equal(s.apiStops(),1);assert.equal(s.counts().stops,0);
 }finally{await s.remove();}
});

test('retained reads reject foreign or recreated actors and changed parent or worker identity',async()=>{
 for(const mutate of [
  (s:Awaited<ReturnType<typeof setup>>)=>s.onActor(async()=>{throw new Error('canonical actor incarnation changed');}),
  (s:Awaited<ReturnType<typeof setup>>)=>s.onActor(async()=>s.replaceParent()),
  (s:Awaited<ReturnType<typeof setup>>)=>s.onInspect(value=>({...value,generation:'foreign-generation'})),
  (s:Awaited<ReturnType<typeof setup>>)=>s.onInspect(value=>({...value,parentPid:99})),
  (s:Awaited<ReturnType<typeof setup>>)=>s.onActor(async()=>s.onInspect(value=>({...value,argvSha256:'a'.repeat(64)})))
 ]){
  const s=await setup();try{
   const fact=(await s.registry.refresh(signal()))[0]!;mutate(s);
   await assert.rejects(s.registry.readRetained(fact.id,fact.generation,signal()));
   assert.equal(s.counts().captures,1);assert.equal(s.apiStops(),0);assert.equal(s.counts().stops,0);
  }finally{await s.remove();}
 }
});

test('retained reads serialize against discovery and stop, release after abort and never replay the actor',async()=>{
 const s=await setup(true);let release!:()=>void;try{
  const fact=(await s.registry.refresh(signal()))[0]!,controller=new AbortController();
  let entered!:()=>void;const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  s.onActor(async()=>{entered();await blocked;});
  const read=s.registry.readRetained(fact.id,fact.generation,controller.signal);await ready;
  await assert.rejects(s.registry.refresh(signal()));
  await assert.rejects(s.registry.stop(fact.id,fact.generation,'owned-serve',signal()));
  await assert.rejects(s.registry.readRetained(fact.id,fact.generation,signal()));
  controller.abort();release();await assert.rejects(read);
  s.onActor(async()=>{});assert.equal((await s.registry.readRetained(fact.id,fact.generation,signal())).after.state,'running');
  assert.equal(s.counts().captures,1);assert.equal(s.apiStops(),0);assert.equal(s.counts().stops,0);
 }finally{release?.();await s.remove();}
});

test('retained reads reject changed physical roots and cannot report an exited process as running again',async()=>{
 const s=await setup(true);try{
  const fact=(await s.registry.refresh(signal()))[0]!;await s.registry.stop(fact.id,fact.generation,'owned-serve',signal());
  let inspections=0;s.onInspect(value=>++inspections===1?value:{...value,state:'running'});
  await assert.rejects(s.registry.readRetained(fact.id,fact.generation,signal()));s.onInspect(undefined);
  await fs.rename(s.config,s.config+'-retained');await fs.symlink(s.config+'-retained',s.config);
  await assert.rejects(s.registry.readRetained(fact.id,fact.generation,signal()));
  assert.equal(s.counts().captures,1);assert.equal(s.apiStops(),1);assert.equal(s.counts().stops,0);
 }finally{await s.remove();}
});

test('unchanged worker registration cannot rebind its captured parent to a new kernel generation',async()=>{
 const s=await setup();try{
  const fact=(await s.registry.refresh(signal()))[0]!;
  s.replaceParent();s.parent.identity.generation='foreign-parent';
  await assert.rejects(s.registry.refresh(signal()));
  await assert.rejects(s.registry.readRetained(fact.id,fact.generation,signal()));
  assert.equal(s.counts().captures,1);assert.equal(s.apiStops(),0);assert.equal(s.counts().stops,0);
 }finally{await s.remove();}
});
