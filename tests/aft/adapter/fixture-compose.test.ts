import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp,realpath,readdir,rm,readFile,writeFile,mkdir } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry,createCapabilityContext,calculateImplementationPin,revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { loadSuiteFiles,runFiles,type RunnerOptions } from '@tysonthomas9/aft/runner';
import { z } from 'zod';
import { createCoreProviders } from './index.js';
import { createEvidenceStore,putEvidenceStore,readFixtureArtifact } from './evidence.js';
import { putFixture,disposeFixtures,type OwnedFixture } from './ownership.js';
import { defineOperation } from './operation.js';
import { createFixtureOperationAuthority } from './authority.js';
import { ObservationError } from './protocol.js';
import { FixtureComposeServeId,RestartComposeServeId,FixtureComposeServeEffects,RestartComposeServeEffects,
  ComposeServeTarget,ComposeRestartFacts,FixtureComposeServeOutput,RestartComposeServeOutput } from './fixture-compose.js';

async function setup(t:{after(fn:()=>Promise<void>):void}) {
  const directory=fileURLToPath(new URL('.',import.meta.url));
  const files=(await readdir(directory)).filter(file=>file.endsWith('.ts')&&!file.endsWith('.test.ts'));
  const pin=calculateImplementationPin(directory,files,'index.ts','createCoreProviders'),registry=new CapabilityRegistry();
  for(const provider of createCoreProviders(pin))registry.register(provider);
  const context=createCapabilityContext({file:'compose-root.test.yaml',line:1},registry),abort=new AbortController();context.signal=abort.signal;
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-compose-contract-')));
  t.after(()=>rm(root,{recursive:true}));const store=await createEvidenceStore(root);putEvidenceStore(context,store);
  let verifies=0,reads=0,restarts=0,mutations=0;
  const forbidden=()=>{assert.fail('Unit producer does not perform real Compose/API/OS/provider transport');};
  const fixture:OwnedFixture={leaseId:'lease',runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,
    scope:context.scope,profile:'agents-real-opencode',workspaceId:'workspace',repo:'/owned',evidenceClass:'deterministic',
    expiresAtUtcMs:Date.now()+100000,roots:new Map(),agents:new Map(),secrets:['never-publish-this'],
    readApi:forbidden,readFiles:forbidden,resolveAgent:forbidden,verify:async()=>{verifies++;},dispose:async()=>{}};
  const inventory={text:'21 loom\n22 opencode\n',complete:true as const,redaction:{omittedPaths:[],replacedTextPaths:[]}};
  const target=ComposeServeTarget.parse({fixtureLeaseId:fixture.leaseId,kind:'compose-container',service:'loom-local',scope:'loom-local-plus-OpenCode',
    project:'owned-project',fixtureRunId:'actual-source-run',container:{containerId:'owned-old',initPid:21,startedAt:'original-start',generation:'original-generation'},
    imageId:'sha256:'+'a'.repeat(64),namespaceSha256:'b'.repeat(64),processInventory:inventory});
  const facts=ComposeRestartFacts.parse({fixtureLeaseId:fixture.leaseId,scope:'loom-local-plus-OpenCode',before:target.container,
    after:{containerId:'owned-successor',initPid:31,startedAt:'successor-start',generation:'successor-generation'},
    successor:{predecessorContainerId:target.container.containerId,containerIdChanged:true,namespaceSha256:target.namespaceSha256},
    readiness:{path:'/api/config',status:204,complete:true,attempts:2,elapsedMs:180200,
      clockPolicy:{unit:'integer-seconds',deadlineEvaluation:'after-failed-request',windowSeconds:180,requestTimeoutMs:3000,cadenceMs:1000},
      startedAtSeconds:100,deadlineAtSeconds:280,completedAtSeconds:280},
    processInventory:{before:{...inventory,text:'21 loom\n22 opencode\n23 changed-live-process\n'},after:{...inventory,text:'31 loom\n32 opencode\n'}},
    sourceRestrictions:{dispatchTimeoutMs:60000,maximumReadinessBodyBytes:4194304}});
  const grant=()=>createFixtureOperationAuthority(fixture,{
    [FixtureComposeServeId]:{evidenceClass:'deterministic',effects:[...FixtureComposeServeEffects]},
    [RestartComposeServeId]:{evidenceClass:'deterministic',effects:[...RestartComposeServeEffects]}});
  fixture.operationAuthority=grant();
  fixture.observeComposeServe=async()=>{reads++;return structuredClone(target);};
  fixture.restartComposeServe=async observed=>{
    restarts++;
    if(JSON.stringify(observed.container)!==JSON.stringify(target.container))throw new ObservationError('identity-mismatch','Stale source target');
    mutations++;return structuredClone(facts);
  };
  putFixture(context,fixture);
  const observe=(request:unknown={leaseId:fixture.leaseId})=>registry.invoke({id:FixtureComposeServeId,version:1,input:{}},request,context);
  const restart=(request:unknown)=>registry.invoke({id:RestartComposeServeId,version:1,input:{}},request,context);
  const capture=async()=>FixtureComposeServeOutput.parse((await observe()).data);
  return {fixture,target,facts,store,context,abort,grant,registry,pin,root,observe,restart,capture,counts:()=>({verifies,reads,restarts,mutations})};
}

test('public default Compose capture binds an actual receipt and preserves late success and independent process inventory',async t=>{
  const h=await setup(t),capture=await h.capture();h.context.source.line++;
  const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});assert.equal(result.availability,'observed');
  const data=RestartComposeServeOutput.parse(result.data);
  assert.equal(data.scope,'loom-local-plus-OpenCode');assert.equal(data.before.initPid,21);assert.equal(data.after.initPid,31);
  assert.equal(data.readiness.elapsedMs,180200);assert.equal(data.readiness.clockPolicy.deadlineEvaluation,'after-failed-request');
  assert.notDeepEqual(data.processInventory.before,capture.target.processInventory);
  assert.equal(result.provenance.evidenceClass,'deterministic');assert.equal(h.counts().mutations,1);
  const retained=await readFixtureArtifact(h.context,h.fixture.leaseId,data.receipt) as {facts:unknown};assert.deepEqual(retained.facts,h.facts);
  assert.throws(()=>assert.equal(data.readiness.status,500));
});
test('missing external provider permission denies before fixture verification, private getter or restart factory',async t=>{
  const h=await setup(t),capture=await h.capture(),before=h.counts();
  h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[RestartComposeServeId]:{
    evidenceClass:'deterministic',effects:RestartComposeServeEffects.filter(effect=>effect!=='external-provider')}});
  Object.defineProperty(h.fixture,'restartComposeServe',{get(){assert.fail('No private getter without all grants');}});
  const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});assert.equal(result.availability,'unsupported');
  assert.deepEqual(h.counts(),before);
});
for(const missing of ['write-fixture','stop-owned-process'] as const) test(`Compose observation missing only ${missing} denies before getter, verification and artifacts`,async t=>{
  const h=await setup(t),complete=['read-filesystem','start-owned-process','write-fixture','stop-owned-process'] as const;
  h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[FixtureComposeServeId]:{
    evidenceClass:'deterministic',effects:complete.filter(effect=>effect!==missing)}});
  const producer=h.fixture.observeComposeServe;let getters=0,writes=0;
  Object.defineProperty(h.fixture,'observeComposeServe',{get(){getters++;return producer;}});
  const retain=h.store.retain;h.store.retain=async bytes=>{writes++;return retain.call(h.store,bytes);};
  assert.equal((await h.observe()).availability,'unsupported');assert.equal(getters,0);assert.equal(writes,0);
  assert.deepEqual(h.counts(),{verifies:0,reads:0,restarts:0,mutations:0});
});
test('Compose restart missing only write-fixture denies before getter, verification and artifacts',async t=>{
  const h=await setup(t),capture=await h.capture(),before=h.counts();
  const complete=['read-api','read-filesystem','start-owned-process','stop-owned-process','restart-owned-service','external-provider','write-fixture'] as const;
  h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[RestartComposeServeId]:{
    evidenceClass:'deterministic',effects:complete.filter(effect=>effect!=='write-fixture')}});
  const producer=h.fixture.restartComposeServe;let getters=0,writes=0;
  Object.defineProperty(h.fixture,'restartComposeServe',{get(){getters++;return producer;}});
  const retain=h.store.retain;h.store.retain=async bytes=>{writes++;return retain.call(h.store,bytes);};
  assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt})).availability,'unsupported');
  assert.equal(getters,0);assert.equal(writes,0);assert.deepEqual(h.counts(),before);
});
test('unknown receipts, substituted metadata and foreign lease cannot reach the restart producer',async t=>{
  const h=await setup(t),capture=await h.capture();
  for(const request of [
    {leaseId:'foreign',targetReceipt:capture.targetReceipt},
    {leaseId:h.fixture.leaseId,targetReceipt:{...capture.targetReceipt,id:'unknown'}},
    {leaseId:h.fixture.leaseId,targetReceipt:{...capture.targetReceipt,bytes:1}},
    {leaseId:h.fixture.leaseId,targetReceipt:{...capture.targetReceipt,sha256:'0'.repeat(64)}},
  ])assert.equal((await h.restart(request)).availability,'error');
  assert.equal(h.counts().restarts,0);assert.equal(h.counts().mutations,0);
});
test('canonical target receipt enforces case, fixture owner, pin and selector identity before restart',async t=>{
  for(const kind of ['case','fixture','pin','target'] as const) {
    const h=await setup(t),capture=await h.capture();
    const wrapper=await readFixtureArtifact(h.context,h.fixture.leaseId,capture.targetReceipt) as Record<string,unknown>;
    if(kind==='case')(wrapper.owner as {caseId:string}).caseId='foreign-case';
    if(kind==='fixture')(wrapper.fixtureOwner as {profile:string}).profile='legacy-deterministic';
    if(kind==='pin')(wrapper.provenance as {implementationSha256:string}).implementationSha256='0'.repeat(64);
    if(kind==='target')(wrapper.target as {fixtureLeaseId:string}).fixtureLeaseId='foreign';
    const receipt=await h.store.retain(JSON.stringify(wrapper));
    assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:receipt})).availability,'error');
    assert.equal(h.counts().restarts,0);
  }
});
test('tampered receipt bytes and an authoritative oversized record deny before large allocation or restart',async t=>{
  const h=await setup(t),capture=await h.capture();const file=await h.store.resolve(capture.targetReceipt.id);
  const bytes=await readFile(file);bytes[bytes.length-1]=bytes[bytes.length-1]===32?10:32;await writeFile(file,bytes);
  assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt})).availability,'error');
  const large=await h.store.retain(JSON.stringify({text:'x'.repeat(4*1024*1024)}));
  const original=Buffer.alloc;let largeAllocations=0;
  Buffer.alloc=((size:number,...args:unknown[])=>{if(size>4*1024*1024)largeAllocations++;return Reflect.apply(original,Buffer,[size,...args]);}) as typeof Buffer.alloc;
  try {assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:{...large,bytes:1}})).availability,'incomplete');}
  finally {Buffer.alloc=original;}
  assert.equal(largeAllocations,0);assert.equal(h.counts().restarts,0);
});
test('unsupported profile and omitted producers are unavailable without transports and never use host or native fallback',async t=>{
  for(const kind of ['profile','producer'] as const) {
    const h=await setup(t);if(kind==='profile'){h.fixture.profile='legacy-deterministic';h.fixture.operationAuthority=h.grant();}else delete h.fixture.observeComposeServe;
    const result=await h.observe();assert.equal(result.availability,'unsupported');assert.deepEqual(h.counts(),{verifies:0,reads:0,restarts:0,mutations:0});
  }
});
test('Compose authority replacement during verify denies before producer and revoked context cannot use receipt DATA',async t=>{
  for(const kind of ['grant','callback','fixture'] as const) {
    const h=await setup(t);h.fixture.verify=async()=>{
      if(kind==='grant')h.fixture.operationAuthority=h.grant();
      else if(kind==='callback')h.fixture.observeComposeServe=async()=>{assert.fail('Replacement producer');};
      else h.context.resources.set('@loom/aft-adapter/fixtures/v1:lease',{...h.fixture});
    };
    assert.equal((await h.observe()).availability,'error');assert.equal(h.counts().reads,0);
  }
  const h=await setup(t),capture=await h.capture();revokeCapabilityContext(h.context);
  assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt})).availability,'error');
  assert.equal(h.counts().restarts,0);
});
test('Compose restart rechecks grant and callback after producer without issuing a success receipt',async t=>{
  for(const kind of ['grant','callback','abort'] as const) {
    const h=await setup(t),capture=await h.capture(),original=h.fixture.restartComposeServe!;
    h.fixture.restartComposeServe=async(target,signal)=>{const value=await original(target,signal);
      if(kind==='grant')h.fixture.operationAuthority=h.grant();
      else if(kind==='callback')h.fixture.restartComposeServe=original;
      else h.abort.abort();return value;};
    if(kind==='abort')await assert.rejects(h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt}),/aborted/);
    else {const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});assert.equal(result.availability,'error');assert.equal(result.data,undefined);}
  }
});
test('stale container identity is rejected by the owning producer before its mutation',async t=>{
  const h=await setup(t),capture=await h.capture();h.target.container.generation='replacement-generation';
  assert.equal((await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt})).availability,'error');
  assert.equal(h.counts().mutations,0);
});
test('returned foreign successor, namespace or before target cannot credit a successful restart',async t=>{
  for(const kind of ['before','namespace','successor'] as const) {
    const h=await setup(t),capture=await h.capture();
    if(kind==='before')h.facts.before.initPid++;
    if(kind==='namespace')h.facts.successor.namespaceSha256='0'.repeat(64);
    if(kind==='successor')h.facts.successor.containerIdChanged=false;
    const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});
    assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  }
});
test('closed inputs reject command/service overrides and source clock policy preserves late positive but not fabricated policy',async t=>{
  const h=await setup(t);await assert.rejects(h.observe({leaseId:h.fixture.leaseId,service:'foreign'}));
  const capture=await h.capture();await assert.rejects(h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt,command:'restart'}));
  assert.equal(ComposeRestartFacts.safeParse({...h.facts,readiness:{...h.facts.readiness,elapsedMs:190000}}).success,true);
  assert.equal(ComposeRestartFacts.safeParse({...h.facts,readiness:{...h.facts.readiness,deadlineAtSeconds:281}}).success,false);
  assert.equal(ComposeRestartFacts.safeParse({...h.facts,readiness:{...h.facts.readiness,clockPolicy:{...h.facts.readiness.clockPolicy,deadlineEvaluation:'before-request'}}}).success,false);
});
test('private material cannot be silently redacted into Compose identity credit',async t=>{
  const h=await setup(t);h.target.project='never-publish-this';assert.equal((await h.observe()).availability,'incomplete');
});
test('raw declarative loader admits observation to canonical targetReceipt restart reference with effects zero',async t=>{
  const h=await setup(t),file=path.join(h.root,'compose-chain.test.yaml');
  await writeFile(path.join(h.root,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:createCoreProviders(h.pin)
    .map(provider=>({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  await writeFile(file,JSON.stringify({suite:'Compose closed contract',tests:[{name:'observe then restart',steps:[
    {capability:{request:{id:FixtureComposeServeId,version:1,input:{leaseId:{literal:'lease'}}},as:'target'}},
    {capability:{request:{id:RestartComposeServeId,version:1,input:{leaseId:{literal:'lease'},targetReceipt:{ref:{binding:'target',pointer:'/targetReceipt'}}}},as:'restarted'}},
  ]}]}));
  assert.equal(loadSuiteFiles([file],{registry:h.registry,requiredProfile:'declarative'}).length,1);
  assert.deepEqual(h.counts(),{verifies:0,reads:0,restarts:0,mutations:0});
});
test('actual public runner transports the captured Compose receipt and independently rejects a wrong expectation',async t=>{
  const h=await setup(t),oldPath=process.env.PATH,oldHome=process.env.AFT_HOME;
  t.after(async()=>{if(oldPath===undefined)delete process.env.PATH;else process.env.PATH=oldPath;
    if(oldHome===undefined)delete process.env.AFT_HOME;else process.env.AFT_HOME=oldHome;});
  const bin=path.join(h.root,'bin');await mkdir(bin);process.env.PATH=`${bin}:${oldPath??''}`;process.env.AFT_HOME=path.join(h.root,'aft-home');
  // Owned CLI double: no real browser, Compose, product, OS inventory or provider.
  await writeFile(path.join(bin,'agent-browser'),`#!/usr/bin/env node
const args=process.argv.slice(2),at=args.indexOf('--session'),command=args.slice(at+2);
if(command[0]==='get')console.log(command[1]==='url'?'about:blank':'Owned double');
else if(command[0]==='network')console.log(JSON.stringify({requests:[]}));
else if(command[0]==='eval')console.log('null');
`,{mode:0o700});
  const directory=fileURLToPath(new URL('.',import.meta.url));
  const testPin=calculateImplementationPin(directory,['fixture-compose.test.ts'],'fixture-compose.test.ts','test');
  const owners:Parameters<typeof putFixture>[0][]=[];
  const acquire=defineOperation({id:'test.composeFixture',implementation:testPin,implementationSha256:testPin.sha256,
    inputSchema:z.object({}).strict(),outputSchema:z.object({leaseId:z.string()}).strict(),
    effects:['write-fixture'],retry:'never',cleanup:'release-lease',evidenceClasses:['deterministic'],
    async run(_input,context){owners.push(context);Object.assign(h.fixture,{runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,scope:context.scope});
      h.fixture.operationAuthority=h.grant();putEvidenceStore(context,h.store);putFixture(context,h.fixture);
      return {value:{leaseId:h.fixture.leaseId},evidenceClass:'deterministic',identity:{fixtureLeaseId:h.fixture.leaseId}};},
    dispose:disposeFixtures});
  h.registry.register(acquire);
  await writeFile(path.join(h.root,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:[...createCoreProviders(h.pin),acquire]
    .map(provider=>({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  const ref=(binding:string,pointer:string)=>({ref:{binding,pointer}}),literal=(value:unknown)=>({literal:value});
  const chain=(expected:number)=>[
    {capability:{request:{id:acquire.id,version:1,input:{}},as:'fixture'}},
    {capability:{request:{id:FixtureComposeServeId,version:1,input:{leaseId:ref('fixture','/leaseId')}},as:'target'}},
    {capability:{request:{id:RestartComposeServeId,version:1,input:{leaseId:ref('fixture','/leaseId'),targetReceipt:ref('target','/targetReceipt')}},as:'restart'}},
    {assert:{op:'eq',args:[ref('restart','/after/initPid'),literal(expected)]}},
  ];
  const file=path.join(h.root,'public-compose.test.yaml');await writeFile(file,JSON.stringify({suite:'Compose public receipt',tests:[
    {name:'exact target receipt',steps:chain(31)},{name:'incorrect expected PID',steps:chain(99)}]}));
  const options:RunnerOptions={registry:h.registry,requiredProfile:'declarative',mode:'strict',caseHeaders:false,agent:false,headed:false,
    record:false,recordAll:false,screenshots:false,retries:0,stepTimeoutMs:4000,pollIntervalMs:1,budgetWarn:0,budgets:{},
    reportDir:path.join(h.root,'reports'),a11y:false,a11yBaselines:path.join(h.root,'baselines'),a11yImpact:'serious',testidAttribute:'data-testid'};
  const result=await runFiles([file],options);
  assert.equal(result.tests.find(item=>item.name==='exact target receipt')?.status,'passed');
  assert.equal(result.tests.find(item=>item.name==='incorrect expected PID')?.status,'failed');
  assert.equal(h.counts().mutations,2);assert.equal(owners.length,2);
  for(const owner of owners)assert.equal(owner.resources.has('@loom/aft-adapter/fixtures/v1:lease'),false);
});

test('parent: replacing the lease-bound store during observation must not adopt the new store',async t=>{
  const h=await setup(t);
  const foreignRoot=path.join(h.root,'replacement-store');await mkdir(foreignRoot);
  const foreign=await createEvidenceStore(foreignRoot);let writes=0;
  const retain=foreign.retain;foreign.retain=async text=>{writes++;return retain.call(foreign,text);};
  const producer=h.fixture.observeComposeServe!;
  h.fixture.observeComposeServe=async signal=>{
    const value=await producer(signal);
    h.context.resources.set('@loom/aft-adapter/evidence/v1:lease',foreign);
    return value;
  };
  const result=await h.observe();
  console.log(JSON.stringify({availability:result.availability,replacementStoreWrites:writes}));
  assert.notEqual(result.availability,'observed','must deny store replacement before success');
  assert.equal(writes,0,'must not write to newly substituted store');
});

test('parent: replacing the grant during final observation-envelope retention must deny publication',async t=>{
  const h=await setup(t),retain=h.store.retain;let writes=0;
  h.store.retain=async text=>{
    const result=await retain.call(h.store,text);
    if(++writes===2)h.fixture.operationAuthority=h.grant();
    return result;
  };
  const result=await h.observe();
  console.log(JSON.stringify({availability:result.availability,retainedWrites:writes,transition:'grant during final retention'}));
  assert.notEqual(result.availability,'observed','final retention must remain inside authority checks');
});

test('parent: replacing the grant during final restart-envelope retention must deny publication',async t=>{
  const h=await setup(t),capture=await h.capture(),retain=h.store.retain;let writes=0;
  h.store.retain=async text=>{
    const result=await retain.call(h.store,text);
    if(++writes===2)h.fixture.operationAuthority=h.grant();
    return result;
  };
  const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});
  console.log(JSON.stringify({availability:result.availability,retainedWrites:writes,transition:'grant during restart final retention'}));
  assert.notEqual(result.availability,'observed','final retention must remain inside authority checks');
});
test('Compose observation writes both receipts to its pinned lease store when the context-global store changes',async t=>{
  const h=await setup(t);let foreignWrites=0,writes=0;const retain=h.store.retain;
  h.store.retain=async bytes=>{writes++;return retain.call(h.store,bytes);};
  h.context.resources.set('@loom/aft-adapter/evidence/v1',{...h.store,async retain(){foreignWrites++;throw Error('foreign context store');}});
  assert.equal((await h.observe()).availability,'observed');assert.deepEqual([writes,foreignWrites],[2,0]);
});
test('Compose observation rejects store method replacement by its producer before dispatching a write',async t=>{
  const h=await setup(t),producer=h.fixture.observeComposeServe!;let writes=0;
  h.fixture.observeComposeServe=async signal=>{const target=await producer(signal);
    h.store.retain=async()=>{writes++;throw Error('replacement writer');};return target;};
  const result=await h.observe();assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.equal(writes,0);assert.equal(result.provenance.artifacts.length,0);
});
test('Compose restart rejects store replacement during the captured receipt read before opening a new transition',async t=>{
  const h=await setup(t),capture=await h.capture(),resolve=h.store.resolveBounded;let replacementWrites=0;
  h.store.resolveBounded=async(...args)=>{const file=await resolve.call(h.store,...args);
    h.context.resources.set('@loom/aft-adapter/evidence/v1:lease',{...h.store,async retain(){replacementWrites++;throw Error('replacement writer');}});
    return file;};
  const result=await h.restart({leaseId:h.fixture.leaseId,targetReceipt:capture.targetReceipt});
  assert.equal(result.availability,'error');assert.equal(result.data,undefined);assert.equal(h.counts().restarts,0);assert.equal(replacementWrites,0);
});
