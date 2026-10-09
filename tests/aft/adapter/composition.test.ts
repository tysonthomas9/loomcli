import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, writeFile, lstat, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { registerLoomAdapter, pinLoomImplementation, selectLegacyProviderOptions } from './composition.js';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { putFixture, type OwnedFixture } from './ownership.js';
import { createFixtureOperationAuthority } from './authority.js';
import { legacyTaskEffects } from './legacy/effects.js';
import { AcquireOutput } from './fixture/providers.js';
import type { FixturePlan, FixtureDriver } from './fixture/lifecycle.js';

test('one composition registers closed schemas and shares acquired authority with core filesystem observations', async t => {
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-adapter-composition-')));
  t.after(()=>rm(directory,{recursive:true})); await writeFile(path.join(directory,'marker'),'actual local bytes');
  const store = await createEvidenceStore(directory); const stat = await lstat(directory);
  const revision = {repository:'loom',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)};
  const plan: FixturePlan = {profile:'agents-emulator',loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,
    model:'fixture/emulated',caseCount:1,maxCases:1,selectionSha256:'e'.repeat(64),leaseDurationMs:10000};
  const calls: string[] = []; let legacyFactories = 0;
  const driver: FixtureDriver = {preflight:async()=>{calls.push('preflight');},identity:async()=>true,
    allocate:async(_lease,_run,record)=>{record({id:directory,kind:'directory',generation:'owned'});},
    provision:async()=>({apiOrigin:'http://127.0.0.1:4100',filesOrigin:'http://127.0.0.1:4101',workspaceId:'workspace',repo:directory}),
    inspect:async()=>({owned:true,complete:true,services:[]}),remove:async()=>{calls.push('remove');},
    artifact:async(kind,value)=>({...await store.retain(JSON.stringify({kind,value})),redaction:'sanitized' as const})};
  const adapterRoot = fileURLToPath(new URL('.',import.meta.url)); const pin = await pinLoomImplementation(adapterRoot,'source');
  assert.ok(pin.files.some(file=>file.path==='fixture/providers.ts'));
  assert.ok(pin.files.some(file=>file.path==='legacy/scenarios.json'));
  assert.ok(pin.files.some(file=>file.path==='projections/node_modules/unified/index.js'));
  const registry = registerLoomAdapter(new CapabilityRegistry(),{implementation:pin,legacyAccess(){legacyFactories++;throw new Error('unused test transport');},
    fixtures:{plans:[plan],driver:()=>driver,evidenceAfterFailure:async()=>store,bind:async()=>({evidenceClass:'deterministic',evidenceStore:store,
      roots:new Map([['source',{path:directory,device:stat.dev,inode:stat.ino}]]),secrets:[],readApi:async()=>({status:404,body:{}}),
      readFiles:async()=>({status:404,body:{}}),resolveAgent:async()=>{throw new Error('no test agent');}})}});
  const context = createCapabilityContext({file:'composition.test.yaml',line:1},registry);
  const invoke = (id:string,input:unknown)=>registry.invoke({id,version:1,input:{}},input,context);
  const input = {runId:context.runId,profile:plan.profile,loomRevision:revision,fleetRevision:revision,model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256};
  await assert.rejects(invoke('loom.fixture.acquire',{...input,command:'unsafe'})); assert.deepEqual(calls,[]);
  const acquired = await invoke('loom.fixture.acquire',input); assert.equal(acquired.availability,'observed');
  const leaseId = AcquireOutput.parse(acquired.data).lease.id;
  const observed = await invoke('loom.filesystem.observe',{leaseId,rootId:'source',relativePaths:['marker'],view:'bytes',maxBytes:100,maxEntries:1});
  assert.equal(observed.availability,'observed');
  assert.equal((observed.data as {entries:{contentBase64:string}[]}).entries[0]!.contentBase64,Buffer.from('actual local bytes').toString('base64'));
  await assert.rejects(invoke('loom.runtime.stimulate',{leaseId,command:'unsafe'})); assert.equal(legacyFactories,0);
  assert.equal((await invoke('loom.fixture.release',{leaseId})).availability,'observed');
  assert.deepEqual(calls,['preflight','remove']);
});

const routeRevision = {repository:'loom',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)};
const routePlan = (profile: string): FixturePlan => ({profile,loomRevision:routeRevision,fleetRevision:routeRevision,
  engineRevision:routeRevision,adapterRevision:routeRevision,model:'openai/model',maxCases:1,caseCount:1,
  selectionSha256:'e'.repeat(64),leaseDurationMs:10000});

test('trusted composition selects only authorized legacy task routes before factory or driver access', async t => {
  const stub = routePlan('legacy-deterministic');
  const native = routePlan('agents-real-opencode');
  const real = routePlan('legacy-real-codex');
  const external = {...real,liveProvider:{backend:'codex' as const,model:real.model}};
  assert.deepEqual(selectLegacyProviderOptions([stub,native,real]),{taskExecution:'deterministic'});
  assert.deepEqual(selectLegacyProviderOptions([native,real]),{taskExecution:'deterministic'});
  assert.deepEqual(selectLegacyProviderOptions([external,native]),{taskExecution:'live-provider'});
  assert.throws(()=>selectLegacyProviderOptions([stub,external]));
  assert.throws(()=>selectLegacyProviderOptions([stub,stub]));
  assert.throws(()=>selectLegacyProviderOptions([{...real,liveProvider:{backend:'claude',model:real.model}}]));
  assert.throws(()=>selectLegacyProviderOptions([routePlan('unknown-profile')]));
  const root = fileURLToPath(new URL('.',import.meta.url));
  const pin = await pinLoomImplementation(root,'source'); let factoryCalls=0, driverCalls=0;
  const unused = async (): Promise<never> => {throw new Error('Unused test transport');};
  const options = (plans: readonly FixturePlan[]) => ({implementation:pin,legacyAccess(){factoryCalls++;throw new Error('Unused factory');},
    fixtures:{plans,driver(){driverCalls++;throw new Error('Unused driver');},bind:unused,evidenceAfterFailure:unused}});
  assert.throws(()=>registerLoomAdapter(new CapabilityRegistry(),options([stub,external])));
  const deterministic = registerLoomAdapter(new CapabilityRegistry(),options([stub]));
  assert.ok(!deterministic.get('loom.cli.task',1).effects.includes('external-provider'));
  assert.ok(deterministic.get('loom.cli.task',1).effects.includes('start-owned-process'));
  assert.ok(deterministic.get('loom.cli.role',1).effects.includes('start-owned-process'));
  const live = registerLoomAdapter(new CapabilityRegistry(),options([external]));
  assert.ok(live.get('loom.cli.task',1).effects.includes('external-provider'));
  assert.ok(!live.get('loom.cli.role',1).effects.includes('external-provider'));
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-route-evidence-')));
  t.after(()=>rm(directory,{recursive:true}));
  const evidence = await createEvidenceStore(directory);
  for (const [registry, profile, evidenceClass] of [
    [deterministic,external.profile,'live-provider'],
    [live,stub.profile,'deterministic'],
  ] as const) {
    const context = createCapabilityContext({file:'route.test.yaml',line:1},registry);
    putEvidenceStore(context,evidence);
    const fixture: OwnedFixture = {leaseId:'lease',runId:context.runId,suiteId:context.suiteId,scope:context.scope,
      caseId:context.caseId,profile,workspaceId:'WS',repo:'/injected/source',expiresAtUtcMs:Number.MAX_SAFE_INTEGER,
      evidenceClass,roots:new Map(),agents:new Map(),secrets:[],readApi:unused,readFiles:unused,resolveAgent:unused,
      verify:async()=>{},dispose:async()=>{}};
    fixture.operationAuthority = createFixtureOperationAuthority(fixture,{'loom.cli.task':{evidenceClass,
      effects:[...legacyTaskEffects({taskExecution:evidenceClass})]}});
    putFixture(context,fixture);
    const denied = await registry.invoke({id:'loom.cli.task',version:1,input:{}},
      {leaseId:'lease',workspaceId:'WS',agentName:'worker',backend:'codex',mode:'once',issueId:null},context);
    assert.equal(denied.availability,'error'); assert.equal(denied.error?.code,'source-mismatch');
  }
  assert.equal(factoryCalls,0); assert.equal(driverCalls,0);
});
