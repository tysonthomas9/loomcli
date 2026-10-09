import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { CapabilityRegistry, createCapabilityContext, getRegisteredResource, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createFixtureOperationAuthority } from './authority.js';
import { createEvidenceStore, evidenceKey, putEvidenceStore, type EvidenceStore } from './evidence.js';
import { fixturesKey, putFixture, type OwnedFixture } from './ownership.js';
import { beginNativeOperation } from './native-operation-authority.js';
import { NativeOperationEffects, type NativeAuthorizedOperation } from './native-operation-effects.js';

async function rig(t: { after(fn: () => Promise<void>): void }, operation: NativeAuthorizedOperation,
  effects: readonly (typeof NativeOperationEffects)[NativeAuthorizedOperation][number][] = NativeOperationEffects[operation]) {
  const context = createCapabilityContext({file:'native-authority.test.yaml',line:1}, new CapabilityRegistry());
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-native-authority-')));
  t.after(() => rm(directory, {recursive:true,force:true}));
  const underlying = await createEvidenceStore(directory);
  let writes = 0, verifies = 0, resolves = 0;
  const store: EvidenceStore = {...underlying, async retain(serialized) {writes++;return underlying.retain(serialized);}};
  putEvidenceStore(context, store);
  const fixture: OwnedFixture = {
    leaseId:'lease',runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,scope:context.scope,
    workspaceId:'workspace',repo:'/owned/source',profile:'explicit-test-route',
    expiresAtUtcMs:context.clock.epochUtcMs+context.clock.now()+60_000,evidenceClass:'deterministic',
    roots:new Map(),agents:new Map(),secrets:[],
    readApi:async()=>{throw Error('unexpected API');},readFiles:async()=>{throw Error('unexpected files');},
    resolveAgent:async()=>{resolves++;throw Error('unexpected resolution');},
    verify:async signal=>{assert.equal(signal,context.signal);verifies++;},dispose:async()=>{},
  };
  fixture.operationAuthority=createFixtureOperationAuthority(fixture,{[operation]:{evidenceClass:'deterministic',effects:[...effects]}});
  putFixture(context,fixture);
  return {context,fixture,store,underlying,get writes(){return writes;},get verifies(){return verifies;},get resolves(){return resolves;}};
}
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => {resolve=done;});
  return {promise,resolve};
}

for(const operation of Object.keys(NativeOperationEffects) as NativeAuthorizedOperation[]) {
  for(const missing of NativeOperationEffects[operation]) test(`${operation} missing ${missing} denies before verify, resolve and retention`,async t=>{
    const h=await rig(t,operation,NativeOperationEffects[operation].filter(effect=>effect!==missing));
    assert.throws(()=>beginNativeOperation(h.context,'lease',operation),/not authorized/);
    assert.deepEqual([h.verifies,h.resolves,h.writes],[0,0,0]);
  });
  test(`${operation} admits its exact supported test route and retains through the bound store`,async t=>{
    const h=await rig(t,operation),guard=beginNativeOperation(h.context,'lease',operation);
    assert.equal(h.verifies,0);await guard.verify();assert.equal(h.verifies,1);
    // A different context-global store is not the fixture's registered store.
    let foreignWrites=0;
    h.context.resources.set(evidenceKey,{...h.store,async retain(serialized:string){foreignWrites++;return h.store.retain(serialized);}});
    const receipt=await guard.retain('{"actual":"observed"}');
    assert.equal(receipt.bytes,21);assert.equal(foreignWrites,0);assert.equal(h.writes,1);
    assert.ok(await h.underlying.resolve(receipt.id));
  });
}
test('missing, forged and wrong-owner grants cannot verify or retain',async t=>{
  const h=await rig(t,'loom.agent.bind');
  h.fixture.operationAuthority=undefined;
  assert.throws(()=>beginNativeOperation(h.context,'lease','loom.agent.bind'),/no operation authority/);
  h.fixture.operationAuthority={'loom.agent.bind':{evidenceClass:'deterministic',effects:[...NativeOperationEffects['loom.agent.bind']]}};
  assert.throws(()=>beginNativeOperation(h.context,'lease','loom.agent.bind'),/trusted owner/);
  h.fixture.operationAuthority=createFixtureOperationAuthority({...h.fixture,leaseId:'foreign'},
    {'loom.agent.bind':{evidenceClass:'deterministic',effects:[...NativeOperationEffects['loom.agent.bind']]}});
  assert.throws(()=>beginNativeOperation(h.context,'lease','loom.agent.bind'),/trusted owner/);
  assert.deepEqual([h.verifies,h.resolves,h.writes],[0,0,0]);
});
const mutations: Record<string,(h:Awaited<ReturnType<typeof rig>>)=>void> = {
  grant:h=>{h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{'loom.native.observe':{evidenceClass:'deterministic',effects:[...NativeOperationEffects['loom.native.observe']]}});},
  fixture:h=>{h.context.resources.set(`${fixturesKey}:lease`,{...h.fixture});},
  store:h=>{h.context.resources.set(`${evidenceKey}:lease`,{...h.store});},
  verify:h=>{h.fixture.verify=async()=>{};},
  resolver:h=>{h.fixture.resolveAgent=async()=>{throw Error('replacement');};},
  retention:h=>{h.store.retain=async()=>{throw Error('replacement');};},
  workspace:h=>{h.fixture.workspaceId='foreign';},
  repo:h=>{h.fixture.repo='/foreign';},
  expiry:h=>{h.fixture.expiresAtUtcMs+=60_000;},
  expired:h=>{h.fixture.expiresAtUtcMs=0;},
  source:h=>{h.context.source={...h.context.source,file:'foreign.yaml'};},
  signal:h=>{h.context.signal=new AbortController().signal;},
  revoked:h=>{revokeCapabilityContext(h.context);},
};
for(const [name,mutate] of Object.entries(mutations)) test(`pending verification rejects ${name} replacement before later retention`,async t=>{
  const h=await rig(t,'loom.native.observe'),entered=deferred(),release=deferred();
  h.fixture.verify=async signal=>{assert.equal(signal,h.context.signal);entered.resolve();await release.promise;};
  const guard=beginNativeOperation(h.context,'lease','loom.native.observe'),pending=guard.verify();
  await entered.promise;mutate(h);release.resolve();await assert.rejects(pending);
  await assert.rejects(guard.retain('{"late":true}'));assert.equal(h.writes,0);
});
test('retention rechecks after the exact store settles and grants no late receipt',async t=>{
  const h=await rig(t,'loom.native.registration'),entered=deferred(),release=deferred(),retain=h.store.retain;
  h.store.retain=async serialized=>{entered.resolve();await release.promise;return retain(serialized);};
  const guard=beginNativeOperation(h.context,'lease','loom.native.registration'),pending=guard.retain('{"actual":true}');
  await entered.promise;revokeCapabilityContext(h.context);release.resolve();await assert.rejects(pending);
  assert.equal(h.writes,1); // Dispatched file remains owned; cancellation is not an undo claim.
});
test('mismatched retained receipt and oversized canonical JSON fail closed',async t=>{
  const h=await rig(t,'loom.native.registration'),retain=h.store.retain;
  h.store.retain=async serialized=>({...await retain(serialized),sha256:'0'.repeat(64)});
  const guard=beginNativeOperation(h.context,'lease','loom.native.registration');
  await assert.rejects(guard.retain('{"actual":true}'),/does not match/);
  assert.equal(h.writes,1);
  await assert.rejects(guard.retain(JSON.stringify(Array.from({length:50_001},()=>null))));
  // Deliberately invalid JSON over the byte bound must be rejected before parsing.
  await assert.rejects(guard.retain('x'.repeat(4_000_001)),/canonical byte bound/);
  assert.equal(h.writes,1);
});
test('pre-aborted and pending caller cancellation deny without late retention',async t=>{
  const h=await rig(t,'loom.agent.bind'),controller=new AbortController();h.context.signal=controller.signal;
  const entered=deferred(),release=deferred();
  h.fixture.verify=async signal=>{assert.equal(signal,controller.signal);entered.resolve();await release.promise;};
  const guard=beginNativeOperation(h.context,'lease','loom.agent.bind'),pending=guard.verify();
  await entered.promise;controller.abort();release.resolve();await assert.rejects(pending);
  assert.throws(()=>beginNativeOperation(h.context,'lease','loom.agent.bind'));
  await assert.rejects(guard.retain('{"late":true}'));assert.equal(h.writes,0);
});
test('suite borrowing requires the current explicit export and active requesting case',async t=>{
  const h=await rig(t,'loom.native.observe'),registry=new CapabilityRegistry();
  h.context.scope='suite';h.fixture.scope='suite';
  h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{'loom.native.observe':{
    evidenceClass:'deterministic',effects:[...NativeOperationEffects['loom.native.observe']]}});
  const child=createCapabilityContext(h.context.source,registry,h.context.runId);child.suiteId=h.context.suiteId;
  const handles=['lease'];
  child.suite={id:h.context.suiteId,handles,getResource:(key,handle)=>getRegisteredResource(h.context,key,handle)};
  h.fixture.verify=async signal=>{assert.equal(signal,child.signal);};
  const guard=beginNativeOperation(child,'lease','loom.native.observe');await guard.verify();
  handles.splice(0);await assert.rejects(guard.retain('{"unexported":true}'));assert.equal(h.writes,0);
  handles.push('lease');const next=beginNativeOperation(child,'lease','loom.native.observe');
  revokeCapabilityContext(child);await assert.rejects(next.verify());assert.equal(h.writes,0);
  assert.equal(beginNativeOperation(h.context,'lease','loom.native.observe').fixture,h.fixture);
});
