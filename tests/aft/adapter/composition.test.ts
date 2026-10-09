import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, writeFile, lstat, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { registerLoomAdapter, pinLoomImplementation } from './composition.js';
import { createEvidenceStore } from './evidence.js';
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
