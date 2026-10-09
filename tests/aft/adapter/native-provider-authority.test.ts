import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry, calculateImplementationPin, createCapabilityContext, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { loadSuiteFiles } from '@tysonthomas9/aft/runner';
import { createCoreProviders } from './index.js';
import { createFixtureOperationAuthority } from './authority.js';
import { createEvidenceStore, evidenceKey, putEvidenceStore, type EvidenceStore } from './evidence.js';
import { putFixture, type OwnedFixture } from './ownership.js';
import { NativeOperationEffects, type NativeAuthorizedOperation } from './native-operation-effects.js';
import { AgentRow, type NativeAccess, type HttpResponse } from './protocol.js';

const agentRef={fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_owned'};
const request=(id:NativeAuthorizedOperation)=>id==='loom.agent.bind'?{leaseId:'lease',workspaceId:'workspace',agentId:'agt_bound'}:
  id==='loom.native.registration'?{agent:agentRef,maxRegistrations:10}:
    {agent:agentRef,view:'session',nativeSessionId:'ses_owned',nativeRoot:'',expectedGeneration:'generation',maxMessages:200};
function deferred(){let resolve!:()=>void;const promise=new Promise<void>(done=>{resolve=done;});return {promise,resolve};}

// Actual canonical registry and retained files, injected verification/native
// ports. These explicit TEST grants never advertise a production profile.
async function rig(t:{after(fn:()=>Promise<void>):void},operation:NativeAuthorizedOperation,
  effects:readonly (typeof NativeOperationEffects)[NativeAuthorizedOperation][number][]=NativeOperationEffects[operation]) {
  const root=fileURLToPath(new URL('.',import.meta.url)),files=(await readdir(root)).filter(file=>file.endsWith('.ts')&&!file.endsWith('.test.ts'));
  const pin=calculateImplementationPin(root,files,'index.ts','createCoreProviders'),registry=new CapabilityRegistry();
  for(const provider of createCoreProviders(pin))registry.register(provider);
  const context=createCapabilityContext({file:'native-provider-authority.test.yaml',line:1},registry);
  const directory=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-native-provider-')));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  const retained=await createEvidenceStore(directory);
  const counts={verify:0,resolve:0,enroll:0,native:0,getter:0,retain:0};
  const store:EvidenceStore={...retained,async retain(serialized){counts.retain++;return retained.retain(serialized);}};
  putEvidenceStore(context,store);
  const row=AgentRow.parse({agent_id:'agt_owned',workspace_id:'workspace',repo:'/owned/source',worktree_path:'/owned/tree',
    branch:'loom/owned',harness:'opencode',harness_session_id:'ses_owned',harness_session_root:'',parent_agent_id:null,
    root_agent_id:null,created_by_kind:'user',created_by_id:null,preset:'lead',revision:1,state:'idle',running_turn_id:null,
    deleted_at:null,history_purged_at:null,created_at:'actual-creation'});
  const native:NativeAccess={pinnedExecutable:'/owned/opencode',registration:async()=>{counts.native++;return {
    url:'http://127.0.0.1:4123/',password:'private-native',pid:42,generation:'generation',endpointId:'endpoint'};},
  process:async()=>{counts.native++;return {pid:42,generation:'generation',executable:'/owned/opencode',argv:['/owned/opencode','serve','--service']};},
  sessions:async()=>{counts.native++;return [{agent_id:row.agent_id,harness:'opencode',native_root:'',native_id:'ses_owned'}];},
  agent:async()=>{counts.native++;return row;},read:async(route,signal):Promise<HttpResponse>=>{assert.equal(signal,context.signal);counts.native++;
    return {status:200,body:route==='/api/info'?{pid:42}:{data:{id:'ses_owned',metadata:{agent_id:row.agent_id},location:{directory:row.worktree_path}}}};}};
  const owned={row,commonDir:'/owned/source/.git',native};
  Object.defineProperty(owned,'native',{configurable:true,get(){counts.getter++;return native;}});
  const fixture:OwnedFixture={leaseId:'lease',runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,scope:context.scope,
    workspaceId:'workspace',repo:row.repo,profile:'explicit-test-route',expiresAtUtcMs:context.clock.epochUtcMs+context.clock.now()+60_000,
    evidenceClass:'deterministic',roots:new Map(),agents:new Map([[row.agent_id,owned]]),secrets:['private-native'],
    readApi:async()=>{throw Error('unexpected API');},readFiles:async()=>{throw Error('unexpected files');},
    readWorkspaceAgent:async()=>{counts.enroll++;throw Error('unexpected enrollment');},
    resolveAgent:async(id,signal)=>{assert.equal(signal,context.signal);counts.resolve++;return {row:AgentRow.parse({...row,agent_id:id}),commonDir:'/owned/source/.git'};},
    verify:async signal=>{assert.equal(signal,context.signal);counts.verify++;},dispose:async()=>{}};
  fixture.operationAuthority=createFixtureOperationAuthority(fixture,{[operation]:{evidenceClass:'deterministic',effects:[...effects]}});
  putFixture(context,fixture);
  const invoke=()=>registry.invoke({id:operation,version:1,input:{}},request(operation),context);
  return {directory,registry,pin,context,fixture,row,native,owned,store,retained,counts,invoke};
}

for(const id of Object.keys(NativeOperationEffects) as NativeAuthorizedOperation[]) {
  for(const missing of NativeOperationEffects[id])test(`public ${id} missing only ${missing} rejects before every fixture port`,async t=>{
    const h=await rig(t,id,NativeOperationEffects[id].filter(effect=>effect!==missing));
    const observed=await h.invoke();assert.equal(observed.availability,'unsupported');assert.equal(observed.data,undefined);
    assert.deepEqual(h.counts,{verify:0,resolve:0,enroll:0,native:0,getter:0,retain:0});
  });
  test(`public ${id} uses its supported TEST grant and the exact lease-bound store`,async t=>{
    const h=await rig(t,id);let foreignWrites=0;
    h.context.resources.set(evidenceKey,{...h.store,async retain(){foreignWrites++;throw Error('foreign context store');}});
    const observed=await h.invoke();assert.equal(observed.availability,'observed');assert.equal(observed.provenance.evidenceClass,'deterministic');
    assert.equal(observed.provenance.artifacts.length,1);assert.ok(await h.retained.resolve(observed.provenance.artifacts[0]!.id));
    assert.equal(foreignWrites,0);assert.equal(h.counts.retain,1);assert.ok(h.counts.verify>0);
    assert.equal(JSON.stringify(observed).includes('private-native'),false);
  });
  test(`public ${id} denies an ungranted production-shaped profile without touching its ports`,async t=>{
    const h=await rig(t,id);h.fixture.profile='production-host';h.fixture.operationAuthority=undefined;
    assert.equal((await h.invoke()).availability,'unsupported');
    assert.deepEqual(h.counts,{verify:0,resolve:0,enroll:0,native:0,getter:0,retain:0});
  });
}
const mutations:Record<string,(h:Awaited<ReturnType<typeof rig>>)=>void>={
  actor:h=>h.fixture.agents.set('agt_owned',{...h.owned}),
  creation:h=>{h.row.created_at='replacement';},
  source:h=>{h.owned.commonDir='/foreign/.git';},
  rowSource:h=>{h.row.repo='/foreign';},
  method:h=>{h.native.agent=async()=>h.row;},
  executable:h=>{h.native.pinnedExecutable='/replacement';},
  grant:h=>{h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{});},
  secrets:h=>{h.fixture.secrets=['replacement'];},
  revoked:h=>{revokeCapabilityContext(h.context);},
};
for(const [name,mutate] of Object.entries(mutations))test(`public native observation rejects ${name} changed during verification before native reads`,async t=>{
  const h=await rig(t,'loom.native.observe'),entered=deferred(),release=deferred();
  h.fixture.verify=async signal=>{assert.equal(signal,h.context.signal);h.counts.verify++;entered.resolve();await release.promise;};
  const pending=h.invoke();await entered.promise;mutate(h);release.resolve();
  const observed=await pending;assert.equal(observed.availability,'error');assert.equal(observed.data,undefined);
  assert.equal(h.counts.native,0);assert.equal(h.counts.retain,0);
});
test('public native registration rejects a method replacement during its first native read',async t=>{
  const h=await rig(t,'loom.native.registration'),entered=deferred(),release=deferred(),registration=h.native.registration;
  h.native.registration=async()=>{entered.resolve();await release.promise;return registration();};
  const pending=h.invoke();await entered.promise;h.native.sessions=async()=>[];release.resolve();
  assert.equal((await pending).availability,'error');assert.equal(h.counts.native,1);assert.equal(h.counts.retain,0);
});
test('mutable model and updated time do not replace actor incarnation',async t=>{
  const h=await rig(t,'loom.native.observe');h.fixture.verify=async()=>{h.row.model='new/model';h.row.updated_at='later';};
  assert.equal((await h.invoke()).availability,'observed');
});
test('bind never overwrites a binding installed while its resolver was pending',async t=>{
  const h=await rig(t,'loom.agent.bind'),entered=deferred(),release=deferred(),resolve=h.fixture.resolveAgent;
  h.fixture.resolveAgent=async(...args)=>{entered.resolve();await release.promise;return resolve(...args);};
  const pending=h.invoke();await entered.promise;
  const replacement={...h.owned,row:AgentRow.parse({...h.row,agent_id:'agt_bound'})};h.fixture.agents.set('agt_bound',replacement);
  release.resolve();assert.equal((await pending).availability,'error');assert.equal(h.fixture.agents.get('agt_bound'),replacement);
  assert.equal(h.counts.retain,0);
});
for(const replace of [false,true])test(`tentative bind retention failure removes only its own entry (replacement=${replace})`,async t=>{
  const h=await rig(t,'loom.agent.bind'),entered=deferred(),release=deferred();
  h.store.retain=async()=>{h.counts.retain++;entered.resolve();await release.promise;throw Error('uncertain retention');};
  const pending=h.invoke();await entered.promise;assert.ok(h.fixture.agents.has('agt_bound'));
  const replacement={...h.owned,row:AgentRow.parse({...h.row,agent_id:'agt_bound'})};
  if(replace)h.fixture.agents.set('agt_bound',replacement);release.resolve();
  const observed=await pending;assert.equal(observed.availability,'error');assert.equal(observed.data,undefined);
  assert.equal(h.fixture.agents.get('agt_bound'),replace?replacement:undefined);assert.equal(observed.provenance.artifacts.length,0);
});
test('revocation while exact native output retention is pending publishes no result or artifact claim',async t=>{
  const h=await rig(t,'loom.native.registration'),entered=deferred(),release=deferred(),retain=h.store.retain;
  h.store.retain=async serialized=>{entered.resolve();await release.promise;return retain(serialized);};
  const pending=h.invoke();await entered.promise;revokeCapabilityContext(h.context);release.resolve();
  const result=await pending;assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.equal(result.provenance.artifacts.length,0);assert.equal(h.counts.retain,1); // Dispatched file is not undone.
});
test('public raw loader permits explicit native capability steps but rejects capture and await retries',async t=>{
  const h=await rig(t,'loom.native.observe'),providers=createCoreProviders(h.pin),file=path.join(h.directory,'native.test.yaml');
  await writeFile(path.join(h.directory,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:providers
    .map(provider=>({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  for(const id of Object.keys(NativeOperationEffects) as NativeAuthorizedOperation[]) {
    const input=Object.fromEntries(Object.entries(request(id)).map(([key,value])=>[key,{literal:value}]));
    const capability={id,version:1,input},observation={source:'adapter',capability,as:'native'};
    const write=async(step:unknown)=>writeFile(file,JSON.stringify({suite:'Native effect admission',tests:[{name:'one',steps:[step]}]}));
    await write({capability:{request:capability,as:'native'}});
    assert.equal(loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}).length,1);
    await write({capture:observation});assert.throws(()=>loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}),/read-only/);
    await write({await:{observation,satisfies:{op:'eq',args:[{literal:true},{literal:true}]},deadline:{withinMs:100,clockId:'host'}}});
    assert.throws(()=>loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}),/read-only/);
    assert.equal(h.registry.get(id,1).retry,'never');assert.deepEqual(h.registry.get(id,1).effects,[...NativeOperationEffects[id]]);
  }
  assert.deepEqual(h.counts,{verify:0,resolve:0,enroll:0,native:0,getter:0,retain:0});
});
