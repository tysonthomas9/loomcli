import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { spawn } from 'node:child_process';
import { OwnedDescendants, createRegisteredProcessPort, type RegisteredIdentity, type RegisteredProcessHandle } from './descendants.js';
import { FixtureLifecycle, type FixtureDriver, type FixturePlan, type Resource } from './lifecycle.js';
const identity:RegisteredIdentity={pid:501,generation:'kernel-start-1',executable:'/owned/backend',argvSha256:'a'.repeat(64),parentPid:400,configurationRoot:'/owned/config',state:'running'};
const registration={id:'native-session-owned-start-1',pid:identity.pid,executable:identity.executable,configurationRoot:identity.configurationRoot};
function setup(){
 let current={...identity},stops=0,abandoned=0,failStop=false;
 const records:Resource[]=[];
 const handle:RegisteredProcessHandle={identity:{...identity},async inspect(){return {...current};},async stop(){stops++;if(failStop)throw new Error('Bearer private-teardown-token');current.state='exited';},async abandon(){abandoned++;}};
 const descendants=new OwnedDescendants({async capture(pid){assert.equal(pid,501);assert.equal(records[0]!.generation,'unverified:501');return handle;}},resource=>{const previous=records.find(r=>r.id===resource.id);if(previous)previous.generation=resource.generation;else records.push({...resource});});
 return {descendants,records,handle,get stops(){return stops;},get abandoned(){return abandoned;},reparent(){current={...current,parentPid:1};},foreign(){handle.identity={...identity,configurationRoot:'/foreign'};},fail(value:boolean){failStop=value;},exit(){current.state='exited';}};
}
test('registered detached child keeps exact kernel ownership after its parent exits',async()=>{
 const r=setup();await r.descendants.enroll(registration);r.reparent();assert.equal((await r.descendants.inspect(registration.id,identity.generation)).parentPid,1);
 await r.descendants.stop(registration.id,identity.generation);assert.equal(r.stops,1);assert.equal((await r.descendants.inspect(registration.id,identity.generation)).state,'exited');
 await assert.rejects(r.descendants.enroll(registration));
});
test('foreign registration and stale generation never authorize a target kill',async()=>{
 const r=setup();r.foreign();await assert.rejects(r.descendants.enroll(registration));assert.equal(r.abandoned,1);assert.equal(r.stops,0);assert.equal(r.records[0]!.generation,'unverified:501');
 const owned=setup();await owned.descendants.enroll(registration);await assert.rejects(owned.descendants.stop(registration.id,'reused-pid-generation'));assert.equal(owned.stops,0);
});
test('detached cleanup failure retain exact resources and safe diagnostics for retry',async()=>{
 const r=setup();const revision={repository:'test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)};
 const plan:FixturePlan={profile:'legacy-deterministic',loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,model:'aft/m',maxCases:1,caseCount:1,selectionSha256:'e'.repeat(64),leaseDurationMs:1};
 let record:(resource:Resource)=>void=()=>{},releasedParent=false;
 const descendants=new OwnedDescendants({async capture(){return r.handle;}},resource=>record(resource));
 const driver:FixtureDriver={async preflight(){},async identity(){return true;},async allocate(_lease,_run,own){record=own;own({id:'owned-parent',kind:'process',generation:'parent'});},async provision(){await descendants.enroll(registration);return{apiOrigin:'owned',filesOrigin:'owned',workspaceId:'owned',repo:'owned'};},async inspect(resource){return {owned:true,complete:true,services:resource.id===registration.id?[{id:resource.id,pid:identity.pid,generation:resource.generation,state:(await descendants.inspect(resource.id,resource.generation)).state}]:[]};},async remove(resource){if(resource.id===registration.id)await descendants.stop(resource.id,resource.generation);else releasedParent=true;},async artifact(_kind,value){assert.equal(JSON.stringify(value).includes('private-teardown-token'),false);return{id:'safe-receipt',sha256:'f'.repeat(64),bytes:1,mediaType:'application/json',redaction:'sanitized'};}};
 const lifecycle=new FixtureLifecycle([plan],()=>driver,()=>0,()=> 'lease');const acquired=await lifecycle.acquire({runId:'run',profile:plan.profile,loomRevision:revision,fleetRevision:revision,model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256},new AbortController().signal);
 r.reparent();r.fail(true);const failure=await lifecycle.release(acquired.lease.id,'run');assert.equal(failure.released,false);assert.deepEqual(failure.remainingOwnedResources,['owned-parent',registration.id]);assert.equal(releasedParent,false);
 r.fail(false);assert.equal((await lifecycle.release(acquired.lease.id,'run')).released,true);assert.equal(releasedParent,true);
});
test('fixed kernel helper protocol never accepts an executable or command from a registration',async()=>{
 const child=new EventEmitter() as EventEmitter & {stdout:PassThrough;stdin:PassThrough;kill():boolean};child.stdout=new PassThrough();child.stdin=new PassThrough();let kills=0;
 child.kill=()=>{kills++;return true;};const operations:string[]=[];let failOnce=true;
 child.stdin.on('data',bytes=>{const value=JSON.parse(bytes.toString());operations.push(value.operation);if(value.operation==='inspect'||value.operation==='stop')queueMicrotask(()=>{if(value.operation==='stop'&&failOnce){failOnce=false;child.stdout.write(JSON.stringify({error:'cleanup-unverified'})+'\n');}else child.stdout.write(JSON.stringify({...identity,state:value.operation==='stop'?'exited':'running'})+'\n');});});
 child.stdin.on('finish',()=>queueMicrotask(()=>child.emit('close',0)));
 const port=createRegisteredProcessPort('/attested/python','/attested/fixture/kernel-process.py',((executable:string,argv:readonly string[],options:import('node:child_process').SpawnOptions)=>{
  assert.equal(executable,'/attested/python');assert.deepEqual(argv,['/attested/fixture/kernel-process.py','501']);assert.equal(options?.shell,false);
  queueMicrotask(()=>child.stdout.write(JSON.stringify(identity)+'\n'));return child;
 }) as unknown as typeof spawn);
 const handle=await port.capture(501);assert.equal((await handle.inspect()).generation,identity.generation);await assert.rejects(handle.stop());await handle.stop();await handle.stop();assert.deepEqual(operations,['inspect','stop','stop','close']);assert.equal(kills,0);
});

test('graceful registered service operation cannot use force cleanup as its fallback',async()=>{
 const r=setup();await r.descendants.enroll(registration);
 await assert.rejects(r.descendants.terminateGracefully(registration.id,identity.generation));assert.equal(r.stops,0);
 let graceful=0;r.handle.terminateGracefully=async()=>{graceful++;r.exit();};
 await assert.rejects(r.descendants.terminateGracefully(registration.id,'foreign-generation'));assert.equal(graceful,0);
 await r.descendants.terminateGracefully(registration.id,identity.generation);assert.equal(graceful,1);assert.equal(r.stops,0);
});
