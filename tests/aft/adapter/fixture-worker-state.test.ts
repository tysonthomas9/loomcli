import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp,realpath,readdir,rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry,createCapabilityContext,calculateImplementationPin,revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createEvidenceStore,putEvidenceStore } from './evidence.js';
import { createFixtureOperationAuthority } from './authority.js';
import { putFixture,type OwnedFixture } from './ownership.js';
import { createCoreProviders } from './index.js';
import { sha256 } from './protocol.js';
import { FixtureWorkerStateId,FixtureWorkerStateEffects,FixtureWorkerStateInput,FixtureWorkerStateOutput,
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
  const input=FixtureWorkerStateInput.parse({leaseId:fixture.leaseId,workspaceId:fixture.workspaceId,agentName:'worker',workerId:'retained-worker',
    expectedWorkerGeneration:'worker-generation',expectedServeGeneration:'serve-generation',expectedDaemonGeneration:'daemon-generation'});
  const kernel=async(id:string,generation:string,pid:number,state:'running'|'exited'):Promise<WorkerStateKernelFact>=>{
    const executable='/owned/loom',argv=[id];
    return {id,generation,pid,state,executable,argv,parentPid:20,configurationRoot:'/owned/config',
      argvSha256:await sha256([executable,...argv].join('\0')+'\0')};
  };
  const sample=async(state:'running'|'exited')=>({serve:await kernel('serve',input.expectedServeGeneration,21,'running'),
    daemon:await kernel('daemon',input.expectedDaemonGeneration,22,'running'),worker:await kernel(input.workerId,input.expectedWorkerGeneration,23,state)});
  const value=FixtureWorkerStateOutput.parse({fixtureLeaseId:fixture.leaseId,coverage:'retained-builtin-worker-state',
    actor:{kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',leaseId:fixture.leaseId,runId:fixture.runId,
      suiteId:fixture.suiteId,caseId:fixture.caseId,scope:fixture.scope,profile:fixture.profile,workspaceId:fixture.workspaceId,
      name:input.agentName,repo:'/owned',commonDir:'/owned/.git',storeId:'store',storeGeneration:'store-generation',
      parentName:null,createdAt:'actual-created',updatedAt:'mutable-update',assignedRepos:['alpha'],assignedRepoGroups:[]},
    worker:{id:input.workerId,generation:input.expectedWorkerGeneration,kind:'worker',identityKind:'legacy-agent-name',
      workspaceId:fixture.workspaceId,agentId:input.agentName,sessionName:null},before:await sample('exited'),after:await sample('exited'),
    api:{httpStatus:200,complete:true,rows:[{name:input.agentName,state:'stopped',desired_state:'stopped'}],matchedRowCount:1,totalRowCount:2},
    stderr:workerStateStderrFacts({text:'first line\r\ncontrol message worker\npartial',capturedBytes:42,
      transportComplete:true,overflow:false,closed:false,captureProcess:await kernel('daemon',input.expectedDaemonGeneration,22,'running'),
      source:{commit:'a'.repeat(40),sourceManifestSha256:'b'.repeat(64),buildManifestSha256:'c'.repeat(64)}})});
  const grant=()=>createFixtureOperationAuthority(fixture,{[FixtureWorkerStateId]:{evidenceClass:'deterministic',effects:[...FixtureWorkerStateEffects]}});
  fixture.operationAuthority=grant();
  fixture.observeWorkerState=async()=>{reads++;return structuredClone(value);};putFixture(context,fixture);
  const invoke=(request:unknown=input,ctx=context)=>registry.invoke({id:FixtureWorkerStateId,version:1,input:{}},request,ctx);
  return {fixture,value,input,context,invoke,grant,abort,counts:()=>({verifies,reads})};
}
test('public default worker-state operation records exited generation and independent row/log facts without stop replay',async t=>{
  const h=await setup(t),result=await h.invoke();assert.equal(result.availability,'observed');
  const facts=FixtureWorkerStateOutput.parse(result.data);
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
});
test('worker-state rejects unsupported route and malformed selector before transport',async t=>{
  const h=await setup(t);delete h.fixture.observeWorkerState;
  assert.equal((await h.invoke()).availability,'unsupported');assert.deepEqual(h.counts(),{verifies:0,reads:0});
  await assert.rejects(h.invoke({...h.input,logRegex:'marker'}));
  await assert.rejects(h.invoke({...h.input,expectedDaemonGeneration:undefined}));
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
  for(const field of ['expectedWorkerGeneration','expectedServeGeneration','expectedDaemonGeneration'] as const)
    assert.equal((await h.invoke({...h.input,[field]:'foreign'})).availability,'error');
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
test('stderr exact line ranges preserve retractions, empty lines, CRLF and Unicode separators',()=>{
  const text='a\r\n\n😀\u2028z';assert.deepEqual(workerStderrLineRanges(text),[[0,1,3],[3,3,4],[4,6,7],[7,8,8]]);
  assert.deepEqual(workerStderrLineRanges(''),[]);assert.deepEqual(workerStderrLineRanges('a\n'),[[0,1,2]]);
  assert.throws(()=>workerStderrLineRanges('x'.repeat(1_000_001)),/string bound/);
  assert.throws(()=>workerStderrLineRanges('\n'.repeat(10_001)),/line bound/);
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
