import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp,readdir,realpath,rm,writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { CapabilityRegistry,calculateImplementationPin,createCapabilityContext,revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { CapabilityEffect } from '@tysonthomas9/aft/types';
import { loadSuiteFiles } from '@tysonthomas9/aft/runner';
import { createCoreProviders } from './index.js';
import { ArchiveAgentId,ArchiveAgentEffects,ArchiveAgentInput,ArchiveAgentIdentity,assertArchiveAgentTarget } from './agent-archive.js';
import { createEvidenceStore,putEvidenceStore,type EvidenceStore } from './evidence.js';
import { createFixtureOperationAuthority } from './authority.js';
import { putFixture,type OwnedFixture } from './ownership.js';

const agent={fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_owned'};
const input={...agent,
  namePrefixes:['cov-files-actual-run-','cov-files-child-actual-run'],idempotencyKey:'cov-files-actual-run-agt_owned-cleanup'};
// Canonical registry and real retained evidence; injected TEST-only fixed
// archive/verification ports. No supported production profile is implied.
async function rig(t:{after(fn:()=>Promise<void>):void},effects:readonly CapabilityEffect[]=ArchiveAgentEffects) {
  const root=fileURLToPath(new URL('.',import.meta.url)),files=(await readdir(root)).filter(file=>file.endsWith('.ts')&&!file.endsWith('.test.ts'));
  const pin=calculateImplementationPin(root,files,'index.ts','createCoreProviders'),registry=new CapabilityRegistry();
  for(const provider of createCoreProviders(pin))registry.register(provider);
  const context=createCapabilityContext({file:'archive.test.yaml',line:1},registry);
  const directory=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-archive-test-')));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  const evidence=await createEvidenceStore(directory),counts={verify:0,getter:0,readback:0,post:0,retain:0};
  const store:EvidenceStore={...evidence,retain:async serialized=>{counts.retain++;return evidence.retain(serialized);}};
  putEvidenceStore(context,store);
  const row=ArchiveAgentIdentity.parse({agent_id:'agt_owned',workspace_id:'workspace',name:'cov-files-actual-run-owned',
    repo:'/owned/source',harness:'opencode'});
  const fixture:OwnedFixture={leaseId:'lease',runId:context.runId,suiteId:context.suiteId,scope:context.scope,caseId:context.caseId,
    profile:'explicit-test-only',workspaceId:'workspace',repo:row.repo,expiresAtUtcMs:context.clock.epochUtcMs+context.clock.now()+60_000,
    evidenceClass:'deterministic',roots:new Map(),agents:new Map(),secrets:[],
    readApi:async()=>{throw Error('unexpected generic API');},readFiles:async()=>{throw Error('unexpected files');},
    resolveAgent:async()=>{throw Error('unexpected rediscovery');},verify:async()=>{counts.verify++;},dispose:async()=>{}};
  let status=204,current={...row};
  const producer:NonNullable<OwnedFixture['archiveAgent']>=async(request,signal)=>{
    assert.equal(signal,context.signal);counts.readback++;
    const observed=assertArchiveAgentTarget(request,current);counts.post++;
    assert.deepEqual(request.body,{cancel:true});assert.equal(request.requestTimeoutMs,15000);
    assert.equal(request.idempotencyKey,input.idempotencyKey);
    return {agent:request.agent,observed,status,requestTimeoutMs:15000,idempotencyKey:request.idempotencyKey,
      body:{cancel:true},responseJsonParsed:false};
  };
  let callback:OwnedFixture['archiveAgent']=producer;
  Object.defineProperty(fixture,'archiveAgent',{configurable:true,get(){counts.getter++;return callback;},set(value){callback=value;}});
  fixture.operationAuthority=createFixtureOperationAuthority(fixture,{[ArchiveAgentId]:{evidenceClass:'deterministic',effects:[...effects]}});
  putFixture(context,fixture);
  const invoke=()=>registry.invoke({id:ArchiveAgentId,version:1,input:{}},input,context);
  return {context,fixture,row,store,evidence,counts,registry,pin,directory,invoke,
    status(value:number){status=value;},current(value:typeof row){current=value;}};
}

for(const missing of ArchiveAgentEffects)test(`archive missing only ${missing} denies before verification/callback/evidence`,async t=>{
  const h=await rig(t,ArchiveAgentEffects.filter(effect=>effect!==missing));
  assert.equal((await h.invoke()).availability,'unsupported');
  assert.deepEqual(h.counts,{verify:0,getter:0,readback:0,post:0,retain:0});
});
for(const status of [200,204,299,409])test(`archive retains actual HTTP ${status} without interpreting a response body`,async t=>{
  const h=await rig(t);h.status(status);const result=await h.invoke();
  assert.equal(result.availability,'observed');
  assert.deepEqual(result.data,{agent,observed:h.row,status,requestTimeoutMs:15000,
    idempotencyKey:input.idempotencyKey,body:{cancel:true},responseJsonParsed:false,conflictIgnored:status===409});
  assert.equal(h.fixture.agents.size,0);
  assert.equal(h.counts.post,1);assert.equal(h.counts.retain,1);
  assert.ok(await h.evidence.resolve(result.provenance.artifacts[0]!.id));
  const provider=h.registry.get(ArchiveAgentId,1);assert.equal(provider.retry,'never');assert.deepEqual(provider.effects,[...ArchiveAgentEffects]);
});
for(const status of [301,400,404,500])test(`archive HTTP ${status} fails without automatic redispatch`,async t=>{
  const h=await rig(t);h.status(status);const result=await h.invoke();
  assert.equal(result.availability,'error');assert.equal(result.data,undefined);assert.equal(h.counts.post,1);assert.equal(h.counts.retain,0);
});
for(const key of ['agent_id','workspace_id','repo','name','harness'])
  test(`archive fresh ${key} mismatch denies before POST`,async t=>{
    const h=await rig(t);h.current({...h.row,[key]:'foreign'});
    assert.equal((await h.invoke()).availability,'error');assert.equal(h.counts.post,0);assert.equal(h.counts.retain,0);
  });
test('archive accepts the actual current name prefix without binding a native actor',async t=>{
  const h=await rig(t);h.current({...h.row,name:'cov-files-actual-run-renamed'});
  assert.equal((await h.invoke()).availability,'observed');
});
test('archive rejects revoked authority during final retention without claiming the dispatched write was undone',async t=>{
  const h=await rig(t),retain=h.store.retain;
  h.store.retain=async serialized=>{const receipt=await retain(serialized);revokeCapabilityContext(h.context);return receipt;};
  const result=await h.invoke();assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.equal(result.provenance.artifacts.length,0);assert.equal(h.counts.post,1);assert.equal(h.counts.retain,1);
});
test('archive rejects callback replacement across verification before dispatch',async t=>{
  const h=await rig(t);h.fixture.verify=async()=>{h.fixture.archiveAgent=async()=>{throw Error('foreign');};};
  assert.equal((await h.invoke()).availability,'error');assert.equal(h.counts.post,0);assert.equal(h.counts.retain,0);
});
test('archive closes public header/body/path escapes without native metadata',async t=>{
  for(const change of [{idempotencyKey:'key\r\nAuthorization: forged'},{idempotencyKey:''},{body:{reason:'cancelled'}},
    {method:'DELETE'},{path:'/foreign'},{headers:{Authorization:'secret'}},{agentId:'../../foreign'},{agent}])
    assert.equal(ArchiveAgentInput.safeParse({...input,...change}).success,false);
  const h=await rig(t);assert.equal(h.fixture.agents.size,0);
  assert.equal((await h.invoke()).availability,'observed');assert.equal(h.counts.post,1);
});
test('archive final evidence callback cannot publish after a same-grant authority replacement',async t=>{
  const h=await rig(t),retain=h.store.retain;
  h.store.retain=async serialized=>{
    const receipt=await retain(serialized);
    h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[ArchiveAgentId]:{
      evidenceClass:'deterministic',effects:[...ArchiveAgentEffects]}});return receipt;
  };
  const result=await h.invoke();assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.equal(result.provenance.artifacts.length,0);assert.equal(h.counts.post,1);assert.equal(h.counts.retain,1);
});
test('raw declarative admission allows explicit archive but rejects capture and await mutation retries',async t=>{
  const h=await rig(t),file=path.join(h.directory,'archive.test.yaml'),providers=createCoreProviders(h.pin);
  await writeFile(path.join(h.directory,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:providers
    .map(provider=>({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  const capability={id:ArchiveAgentId,version:1,input:Object.fromEntries(Object.entries(input).map(([key,value])=>[key,{literal:value}]))};
  const observation={source:'adapter',capability,as:'archive'};
  const write=async(step:unknown)=>writeFile(file,JSON.stringify({suite:'Archive admission',tests:[{name:'one',steps:[step]}]}));
  await write({capability:{request:capability,as:'archive'}});
  assert.equal(loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}).length,1);
  await write({capture:observation});assert.throws(()=>loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}),/read-only/);
  await write({await:{observation,satisfies:{op:'eq',args:[{literal:true},{literal:true}]},deadline:{withinMs:100,clockId:'host'}}});
  assert.throws(()=>loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}),/read-only/);
  assert.deepEqual(h.counts,{verify:0,getter:0,readback:0,post:0,retain:0});
});
