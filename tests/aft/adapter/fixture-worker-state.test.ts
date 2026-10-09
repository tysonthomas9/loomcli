import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp,realpath,readdir,rm,writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry,createCapabilityContext,calculateImplementationPin,revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { loadSuiteFiles } from '@tysonthomas9/aft/runner';
import { createEvidenceStore,putEvidenceStore,getFixtureEvidenceStore } from './evidence.js';
import { createFixtureOperationAuthority } from './authority.js';
import { putFixture,type OwnedFixture } from './ownership.js';
import { createCoreProviders } from './index.js';
import { sha256 } from './protocol.js';
import { FixtureWorkerStateId,FixtureWorkerStateEffects,WorkerStateObservationInput,WorkerStateObservationOutput,WorkerParentObservationOutput,
  workerStateStderrFacts,workerStderrLineRanges,type WorkerStateKernelFact } from './fixture-worker-state.js';

async function setup(t:{after(fn:()=>Promise<void>):void}) {
  const directory=fileURLToPath(new URL('.',import.meta.url));
  const files=(await readdir(directory)).filter(file=>file.endsWith('.ts')&&!file.endsWith('.test.ts'));
  const pin=calculateImplementationPin(directory,files,'index.ts','createCoreProviders'),registry=new CapabilityRegistry();
  for(const provider of createCoreProviders(pin))registry.register(provider);
  const context=createCapabilityContext({file:'worker-state-unit.test.yaml',line:1},registry);
  const abort=new AbortController();context.signal=abort.signal;
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-worker-state-')));
  t.after(()=>rm(root,{recursive:true}));putEvidenceStore(context,await createEvidenceStore(root));
  let verifies=0,reads=0;const transports=()=>{assert.fail('Unit callback does not perform Host I/O or replay stop');};
  const fixture:OwnedFixture={leaseId:'lease',runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,
    scope:context.scope,profile:'deterministic',evidenceClass:'deterministic',expiresAtUtcMs:Date.now()+100000,
    workspaceId:'workspace',repo:'/owned',roots:new Map(),agents:new Map(),secrets:[],
    readApi:transports,readFiles:transports,resolveAgent:transports,verify:async()=>{verifies++;},dispose:async()=>{}};
  const input=WorkerStateObservationInput.parse({view:'state',leaseId:fixture.leaseId,workspaceId:fixture.workspaceId,agentName:'worker',workerId:'retained-worker',
    expectedWorkerGeneration:'worker-generation',parents:{
      serve:{handle:{id:'serve',pid:21,generation:'serve-handle-generation'},kernel:{id:'registered-parent-serve',pid:21,generation:'serve-kernel-generation'}},
      daemon:{handle:{id:'daemon',pid:22,generation:'daemon-handle-generation'},kernel:{id:'registered-parent-daemon',pid:22,generation:'daemon-kernel-generation'}}}});
  const kernel=async(id:string,generation:string,pid:number,state:'running'|'exited'):Promise<WorkerStateKernelFact>=>{
    const executable='/owned/loom',argv=[id];
    return {id,generation,pid,state,executable,argv,parentPid:20,configurationRoot:'/owned/config',
      argvSha256:await sha256([executable,...argv].join('\0')+'\0')};
  };
  const sample=async(state:'running'|'exited')=>({serve:await kernel(input.parents.serve.kernel.id,input.parents.serve.kernel.generation,21,'running'),
    daemon:await kernel(input.parents.daemon.kernel.id,input.parents.daemon.kernel.generation,22,'running'),worker:await kernel(input.workerId,input.expectedWorkerGeneration,23,state)});
  const value=WorkerStateObservationOutput.parse({fixtureLeaseId:fixture.leaseId,coverage:'retained-builtin-worker-state',parents:input.parents,
    actor:{kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',leaseId:fixture.leaseId,runId:fixture.runId,
      suiteId:fixture.suiteId,caseId:fixture.caseId,scope:fixture.scope,profile:fixture.profile,workspaceId:fixture.workspaceId,
      name:input.agentName,repo:'/owned',commonDir:'/owned/.git',storeId:'store',storeGeneration:'store-generation',
      parentName:null,createdAt:'actual-created',updatedAt:'mutable-update',assignedRepos:['alpha'],assignedRepoGroups:[]},
    worker:{id:input.workerId,generation:input.expectedWorkerGeneration,kind:'worker',identityKind:'legacy-agent-name',
      workspaceId:fixture.workspaceId,agentId:input.agentName,sessionName:null},before:await sample('exited'),after:await sample('exited'),
    api:{httpStatus:200,complete:true,rows:[{name:input.agentName,state:'stopped',desired_state:'stopped'}],matchedRowCount:1,totalRowCount:2},
    stderr:workerStateStderrFacts({text:'first line\r\ncontrol message worker\npartial',capturedBytes:42,
      transportComplete:true,overflow:false,closed:false,captureProcess:await kernel(input.parents.daemon.kernel.id,input.parents.daemon.kernel.generation,22,'running'),
      source:{commit:'a'.repeat(40),sourceManifestSha256:'b'.repeat(64),buildManifestSha256:'c'.repeat(64)}})});
  const grant=()=>createFixtureOperationAuthority(fixture,{[FixtureWorkerStateId]:{evidenceClass:'deterministic',effects:[...FixtureWorkerStateEffects]}});
  fixture.operationAuthority=grant();
  const parentValue=WorkerParentObservationOutput.parse({fixtureLeaseId:fixture.leaseId,coverage:'retained-builtin-worker-parent-bindings',
    before:structuredClone(input.parents),after:structuredClone(input.parents)});
  fixture.observeWorkerState=async request=>{reads++;return structuredClone(request.view==='parents'?parentValue:value);};putFixture(context,fixture);
  const invoke=(request:unknown=input,ctx=context)=>registry.invoke({id:FixtureWorkerStateId,version:1,input:{}},request,ctx);
  return {fixture,value,parentValue,input,context,invoke,grant,abort,registry,pin,counts:()=>({verifies,reads})};
}
test('public default worker-state operation records exited generation and independent row/log facts without stop replay',async t=>{
  const h=await setup(t),result=await h.invoke();assert.equal(result.availability,'observed');
  const facts=WorkerStateObservationOutput.parse(result.data);
  assert.equal(facts.before.worker.state,'exited');assert.equal(facts.after.worker.generation,h.input.expectedWorkerGeneration);
  assert.equal(facts.api.rows[0]!.desired_state,'stopped');assert.equal(facts.stderr.prefix,true);
  assert.equal(facts.stderr.lines.length,3);assert.equal(result.provenance.identity.agentId,'worker');
  assert.deepEqual(h.counts(),{verifies:1,reads:1});
});
test('worker-state can observe a running worker before the single stimulus independently of a later read',async t=>{
  const h=await setup(t);h.value.before.worker.state='running';h.value.after.worker.state='running';
  h.value.api.rows[0]!.state='running';h.value.api.rows[0]!.desired_state='running';
  assert.equal((await h.invoke()).availability,'observed');
  h.value.before.worker.state='exited';h.value.after.worker.state='exited';h.value.api.rows[0]!.state='stopped';
  assert.equal((await h.invoke()).availability,'observed');assert.equal(h.counts().reads,2);
});
test('worker-state denies missing effect authority before private producer getter or verification',async t=>{
  const h=await setup(t);h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[FixtureWorkerStateId]:{evidenceClass:'deterministic',effects:['read-api','read-filesystem']}});
  let getters=0;Object.defineProperty(h.fixture,'observeWorkerState',{get(){getters++;assert.fail('No grant');}});
  assert.equal((await h.invoke()).availability,'unsupported');assert.equal(getters,0);assert.deepEqual(h.counts(),{verifies:0,reads:0});
  assert.equal((await h.invoke({view:'parents',leaseId:h.fixture.leaseId})).availability,'unsupported');
  assert.equal(getters,0);assert.deepEqual(h.counts(),{verifies:0,reads:0});
});
for(const missing of ['write-fixture','stop-owned-process'] as const) test(`worker-state missing only ${missing} denies state and parents before getter, verification and artifacts`,async t=>{
  const h=await setup(t);
  const complete=['read-api','read-filesystem','start-owned-process','write-fixture','stop-owned-process'] as const;
  h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[FixtureWorkerStateId]:{
    evidenceClass:'deterministic',effects:complete.filter(effect=>effect!==missing)}});
  const producer=h.fixture.observeWorkerState;let getters=0,writes=0;
  Object.defineProperty(h.fixture,'observeWorkerState',{get(){getters++;return producer;}});
  const store=getFixtureEvidenceStore(h.context,h.fixture.leaseId),retain=store.retain;
  store.retain=async bytes=>{writes++;return retain.call(store,bytes);};
  for(const request of [h.input,{view:'parents',leaseId:h.fixture.leaseId}]) {
    assert.equal((await h.invoke(request)).availability,'unsupported');
    assert.equal(getters,0);assert.equal(writes,0);assert.deepEqual(h.counts(),{verifies:0,reads:0});
  }
});
test('worker-state rejects unsupported route and malformed selector before transport',async t=>{
  const h=await setup(t);delete h.fixture.observeWorkerState;
  assert.equal((await h.invoke()).availability,'unsupported');assert.deepEqual(h.counts(),{verifies:0,reads:0});
  await assert.rejects(h.invoke({...h.input,logRegex:'marker'}));
  await assert.rejects(h.invoke({...h.input,parents:undefined}));
});
test('worker-state refuses foreign lease and canonical context revocation before callback',async t=>{
  const h=await setup(t);assert.equal((await h.invoke({...h.input,leaseId:'foreign'})).availability,'error');
  revokeCapabilityContext(h.context);assert.equal((await h.invoke()).availability,'error');assert.deepEqual(h.counts(),{verifies:0,reads:0});
});
test('worker-state rechecks exact callback and grant after awaited verification',async t=>{
  for(const kind of ['callback','grant','fixture'] as const) {
    const h=await setup(t);h.fixture.verify=async()=>{
      if(kind==='callback')h.fixture.observeWorkerState=async()=>{assert.fail('Replaced callback');};
      else if(kind==='grant')h.fixture.operationAuthority=h.grant();
      else h.context.resources.set('@loom/aft-adapter/fixtures/v1:lease',{...h.fixture});
    };
    assert.equal((await h.invoke()).availability,'error');assert.equal(h.counts().reads,0);
  }
});
test('worker-state refuses revoked, replaced and aborted authority after callback with no output credit',async t=>{
  for(const kind of ['callback','grant','context','abort'] as const) {
    const h=await setup(t),original=h.fixture.observeWorkerState!;
    h.fixture.observeWorkerState=async(input,signal)=>{const value=await original(input,signal);
      if(kind==='callback')h.fixture.observeWorkerState=original;
      else if(kind==='grant')h.fixture.operationAuthority=h.grant();
      else if(kind==='context')revokeCapabilityContext(h.context);
      else h.abort.abort();
      return value;};
    if(kind==='abort')await assert.rejects(h.invoke(),/aborted/);
    else {const result=await h.invoke();assert.equal(result.availability,'error');assert.equal(result.data,undefined);}
  }
});
test('worker-state rejects each foreign target generation and actor owner before crediting facts',async t=>{
  const h=await setup(t);
  assert.equal((await h.invoke({...h.input,expectedWorkerGeneration:'foreign'})).availability,'error');
  for(const kind of ['serve','daemon'] as const)for(const identity of ['handle','kernel'] as const) {
    const input=structuredClone(h.input);input.parents[kind][identity].generation='foreign';
    assert.equal((await h.invoke(input)).availability,'error');
  }
  h.value.actor.runId='foreign';assert.equal((await h.invoke()).availability,'error');
});
test('worker-state refuses changed kernel identity, arguments, resurrection and foreign capture source',async t=>{
  for(const kind of ['pid','argv','resurrection','capture'] as const) {
    const h=await setup(t);
    if(kind==='pid')h.value.after.daemon.pid++;
    if(kind==='argv')h.value.before.worker.argv=['foreign'];
    if(kind==='resurrection')h.value.after.worker.state='running';
    if(kind==='capture')h.value.stderr.captureProcess.generation='foreign';
    assert.equal((await h.invoke()).availability,'error');
  }
});
test('worker-state API count, completeness and exact actor matching cannot be inferred from sidecar state',async t=>{
  const h=await setup(t);h.value.api.rows=[];h.value.api.matchedRowCount=0;
  const empty=await h.invoke();assert.equal(empty.availability,'observed');
  h.value.api.matchedRowCount=1;assert.equal((await h.invoke()).availability,'error');
  h.value.api.rows=[{name:'foreign',state:'stopped',desired_state:'stopped'}];assert.equal((await h.invoke()).availability,'error');
});
test('stderr LF-only ranges preserve literal CR, Unicode and control content for the source grep oracle',()=>{
  const text='a\r\n\n😀\u2028z';assert.deepEqual(workerStderrLineRanges(text),[[0,2,3],[3,3,4],[4,8,8]]);
  const sameLine='agent stopped via control socket\r\u2028\u0085\v\f\x1cworktree=worker';
  assert.deepEqual(workerStderrLineRanges(sameLine),[[0,sameLine.length,sameLine.length]]);
  assert.deepEqual(workerStderrLineRanges(''),[]);assert.deepEqual(workerStderrLineRanges('a\n'),[[0,1,2]]);
  assert.throws(()=>workerStderrLineRanges('x'.repeat(1_000_001)),/string bound/);
  assert.throws(()=>workerStderrLineRanges('\n'.repeat(10_001)),/line bound/);
});
test('public parent bootstrap preserves distinct handle and kernel generations for the subsequent state read',async t=>{
  const h=await setup(t);const observed=await h.invoke({view:'parents',leaseId:h.fixture.leaseId});
  assert.equal(observed.availability,'observed');
  const parents=WorkerParentObservationOutput.parse(observed.data).after;
  assert.equal(parents.serve.handle.generation,'serve-handle-generation');assert.equal(parents.serve.kernel.generation,'serve-kernel-generation');
  assert.equal(parents.daemon.handle.generation,'daemon-handle-generation');assert.equal(parents.daemon.kernel.generation,'daemon-kernel-generation');
  const result=await h.invoke({...h.input,parents});assert.equal(result.availability,'observed');
  const facts=WorkerStateObservationOutput.parse(result.data);
  assert.equal(facts.before.daemon.id,'registered-parent-daemon');assert.equal(facts.stderr.captureProcess.generation,parents.daemon.kernel.generation);
  assert.deepEqual(h.counts(),{verifies:2,reads:2});
  const normalized=structuredClone(parents);normalized.daemon.kernel.generation=normalized.daemon.handle.generation;
  assert.equal((await h.invoke({...h.input,parents:normalized})).availability,'error');
});
test('parent bootstrap rejects changed handle, kernel, PID, swapped parent and another view without granting identity',async t=>{
  for(const kind of ['handle','kernel','pid','swap','view'] as const) {
    const h=await setup(t);
    if(kind==='handle')h.parentValue.after.daemon.handle.generation='replacement';
    if(kind==='kernel')h.parentValue.after.daemon.kernel.generation='replacement';
    if(kind==='pid')h.parentValue.after.daemon.kernel.pid++;
    if(kind==='swap')h.parentValue.after.daemon.handle.id='serve';
    if(kind==='view')h.fixture.observeWorkerState=async()=>structuredClone(h.value);
    const result=await h.invoke({view:'parents',leaseId:h.fixture.leaseId});
    assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  }
});
test('copied parent DATA does not grant a foreign or revoked fixture and absent view fails before callback',async t=>{
  const h=await setup(t),observed=await h.invoke({view:'parents',leaseId:h.fixture.leaseId});
  const parents=WorkerParentObservationOutput.parse(observed.data).after;
  const before=h.counts();await assert.rejects(h.invoke({...h.input,view:undefined}));
  assert.deepEqual(h.counts(),before);
  assert.equal((await h.invoke({...h.input,leaseId:'foreign',parents})).availability,'error');
  revokeCapabilityContext(h.context);assert.equal((await h.invoke({...h.input,parents})).availability,'error');
  assert.deepEqual(h.counts(),before);
});
test('public raw loader admits the captured parent subobject reference without running a fixture or worker effect',async t=>{
  const h=await setup(t),root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-worker-parent-admission-')));
  t.after(()=>rm(root,{recursive:true}));
  await writeFile(path.join(root,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:createCoreProviders(h.pin)
    .map(provider=>({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  const literal=(value:unknown)=>({literal:value});
  const file=path.join(root,'parent-chain.test.yaml');
  const stateInput=Object.fromEntries(Object.entries(h.input).map(([key,value])=>[key,key==='parents'
    ?{ref:{binding:'parents',pointer:'/after'}}:literal(value)]));
  await writeFile(file,JSON.stringify({suite:'Parent binding admission',tests:[{name:'observed parents then state',steps:[
    {capability:{request:{id:FixtureWorkerStateId,version:1,input:{view:literal('parents'),leaseId:literal(h.fixture.leaseId)}},as:'parents'}},
    {capability:{request:{id:FixtureWorkerStateId,version:1,input:stateInput},as:'state'}},
  ]}]}));
  const loaded=loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'});
  assert.equal(loaded.length,1);assert.equal(loaded[0]!.kind,'linear');assert.deepEqual(h.counts(),{verifies:0,reads:0});
});
test('worker-state redaction leaves canonical facts but prohibits same-line source credit',async t=>{
  const h=await setup(t);const safe=workerStateStderrFacts({...h.value.stderr,text:'Bearer private-value'},[]);
  assert.equal(safe.text,'[REDACTED]');assert.deepEqual(safe.redaction.replacedTextPaths,['']);h.value.stderr=safe;
  const result=await h.invoke();assert.equal(result.availability,'incomplete');assert.equal(result.data,undefined);
});
test('worker-state rejects fabricated line offsets and contradictory output closure',async t=>{
  const h=await setup(t);h.value.stderr.lines[0]![1]++;
  assert.equal((await h.invoke()).availability,'error');
  h.value.stderr.lines=workerStderrLineRanges(h.value.stderr.text);h.value.stderr.closed=true;
  assert.equal((await h.invoke()).availability,'error');
});
test('worker-state incomplete transport and overflow cannot credit a truncated stderr prefix',async t=>{
  for(const kind of ['transport','overflow'] as const) {
    const h=await setup(t);
    if(kind==='transport')h.value.stderr.transportComplete=false;else h.value.stderr.overflow=true;
    const result=await h.invoke();assert.equal(result.availability,'incomplete');assert.equal(result.data,undefined);
  }
});
test('worker-state validates captured stderr bytes independently from line and closure facts',async t=>{
  const h=await setup(t);h.value.stderr.capturedBytes++;
  assert.equal((await h.invoke()).availability,'error');
});

for(const view of ['parents','state'] as const)test(`parent: worker ${view} final retention must deny changed grant`,async t=>{
  const h=await setup(t),store=getFixtureEvidenceStore(h.context,h.fixture.leaseId),retain=store.retain;let writes=0;
  store.retain=async text=>{const result=await retain.call(store,text);writes++;h.fixture.operationAuthority=h.grant();return result;};
  const result=await h.invoke(view==='state'?h.input:{view:'parents',leaseId:h.fixture.leaseId});
  console.log(JSON.stringify({view,availability:result.availability,retainedWrites:writes}));
  assert.notEqual(result.availability,'observed','final receipt write must retain exact grant authority');
});
for(const view of ['parents','state'] as const)test(`worker ${view} rejects a writer method replaced by its producer before dispatch`,async t=>{
  const h=await setup(t),producer=h.fixture.observeWorkerState!,store=getFixtureEvidenceStore(h.context,h.fixture.leaseId);let writes=0;
  h.fixture.observeWorkerState=async(input,signal)=>{const result=await producer(input,signal);
    store.retain=async()=>{writes++;throw Error('replacement writer');};return result;};
  const result=await h.invoke(view==='state'?h.input:{view:'parents',leaseId:h.fixture.leaseId});
  assert.equal(result.availability,'error');assert.equal(result.data,undefined);assert.equal(writes,0);assert.equal(result.provenance.artifacts.length,0);
});
test('worker parents output retains only in the registered lease store despite a substituted context-global store',async t=>{
  const h=await setup(t),store=getFixtureEvidenceStore(h.context,h.fixture.leaseId),retain=store.retain;let writes=0,foreignWrites=0;
  store.retain=async bytes=>{writes++;return retain.call(store,bytes);};
  h.context.resources.set('@loom/aft-adapter/evidence/v1',{...store,async retain(){foreignWrites++;throw Error('foreign writer');}});
  const result=await h.invoke({view:'parents',leaseId:h.fixture.leaseId});assert.equal(result.availability,'observed');assert.deepEqual([writes,foreignWrites],[1,0]);
});
