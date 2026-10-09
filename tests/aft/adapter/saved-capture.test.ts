import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, readdir, realpath, rm, readFile, writeFile, link } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry, calculateImplementationPin, createCapabilityContext, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { AgentRow, type HttpResponse, type Json } from './protocol.js';
import { createCoreProviders } from './index.js';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { putFixture, type OwnedFixture } from './ownership.js';
import { defineOperation } from './operation.js';
import { SavedEventsOutput } from './events.js';
import { SavedCaptureInput, rereadSavedCapture } from './saved-capture.js';

const agent={fixtureLeaseId:'capture-lease',workspaceId:'workspace',agentId:'agt_capture'};
const query={agent,after:0,pageSize:2,maxPages:2,maxRecords:10,kinds:[]};
async function setup(t:{after(fn:()=>Promise<void>):void}) {
  const root=fileURLToPath(new URL('.',import.meta.url));
  const files=(await readdir(root)).filter(file=>file.endsWith('.ts')&&!file.endsWith('.test.ts'));
  const pin=calculateImplementationPin(root,files,'index.ts','createCoreProviders');
  const registry=new CapabilityRegistry();let expectedPin=pin.sha256;
  const reread=defineOperation({id:'test.savedCapture',implementation:pin,implementationSha256:pin.sha256,
    inputSchema:SavedCaptureInput,outputSchema:SavedEventsOutput,effects:['read-api','read-filesystem'],retry:'never',cleanup:'none',
    evidenceClasses:['deterministic'],async run(input,context) {
      const result=await rereadSavedCapture(context,input,expectedPin);
      return {value:result.data,evidenceClass:'deterministic'};
    }});
  for(const provider of [...createCoreProviders(pin),reread])registry.register(provider);
  const context=createCapabilityContext({file:'saved-capture.test.yaml',line:1},registry);
  const evidenceRoot=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-saved-capture-')));
  t.after(()=>rm(evidenceRoot,{recursive:true}));
  const store=await createEvidenceStore(evidenceRoot);putEvidenceStore(context,store);
  const row=AgentRow.parse({agent_id:agent.agentId,workspace_id:agent.workspaceId,repo:'/owned/repo',worktree_path:'/owned/tree',
    branch:'loom/capture',harness:'opencode',harness_session_id:'ses_capture',harness_session_root:'',parent_agent_id:null,
    root_agent_id:null,created_by_kind:'user',created_by_id:null,preset:'lead',revision:1,state:'idle',running_turn_id:null,
    deleted_at:null,history_purged_at:null});
  let payload:Json={text:'actual saved answer',password:'private-capture-password'};const routes:string[]=[];
  let response:(route:string)=>HttpResponse=()=>({status:200,body:{events:[{agent_id:row.agent_id,seq:1,event_id:'event_capture',
    kind:'item.completed',turn_id:'turn_capture',payload,created_at:'2026-10-09T00:00:00Z'}],snapshot_seq:1,next:1,more:false}});
  const fixture:OwnedFixture={leaseId:agent.fixtureLeaseId,runId:context.runId,suiteId:context.suiteId,scope:context.scope,caseId:context.caseId,
    workspaceId:row.workspace_id,repo:row.repo,profile:'deterministic',evidenceClass:'deterministic',expiresAtUtcMs:Date.now()+100000,
    roots:new Map(),agents:new Map([[row.agent_id,{row,commonDir:'/owned/repo/.git'}]]),secrets:['private-capture-password'],
    readApi:async route=>{routes.push(route);return response(route);},readFiles:async()=>({status:404,body:{}}),
    resolveAgent:async()=>({row,commonDir:'/owned/repo/.git'}),verify:async()=>{},dispose:async()=>{}};
  putFixture(context,fixture);
  const invoke=(id:string,input:unknown)=>registry.invoke({id,version:1,input:{}},input,context);
  const capture=async()=>{
    const observed=await invoke('loom.api.savedEvents',query);assert.equal(observed.availability,'observed');
    const data=SavedEventsOutput.parse(observed.data);assert.ok(data.captureReceipt);return data.captureReceipt;
  };
  return {context,registry,fixture,store,routes,invoke,capture,wrongPin(){expectedPin='0'.repeat(64);},
    changePayload(value:Json){payload=value;},setResponse(value:typeof response){response=value;}};
}

test('canonical capture retains safe facts and pins the first reread request to the original snapshot',async t=>{
  const h=await setup(t),receipt=await h.capture();
  const bytes=await readFile(await h.store.resolve(receipt.id),'utf8');
  assert.ok(!bytes.includes('private-capture-password'));
  const wrapper=JSON.parse(bytes);assert.deepEqual(wrapper.query,query);assert.equal(wrapper.provenance.source.file,'saved-capture.test.yaml');
  assert.deepEqual(wrapper.data.events[0].redaction.omittedPaths,['/password']);
  h.routes.length=0;
  const observed=await h.invoke('test.savedCapture',{agent,captureReceipt:receipt});assert.equal(observed.availability,'observed');
  const data=SavedEventsOutput.parse(observed.data);assert.match(h.routes[0]!,/snapshot=1/);
  assert.deepEqual(data.events[0]!.payload,{text:'actual saved answer'});
  assert.deepEqual(data.events[0]!.redaction.omittedPaths,['/password']);
  assert.notEqual((data.events[0]!.payload as {text:string}).text,'independent wrong expectation');
  const result=await rereadSavedCapture(h.context,{agent,captureReceipt:receipt},wrapper.provenance.implementationSha256);
  assert.equal(result.redactionAffected,true);
});

test('copied receipt fields, unknown artifact, foreign actor and wrong pin fail before saved API access',async t=>{
  const h=await setup(t),receipt=await h.capture();h.routes.length=0;
  for(const corrupt of [{sha256:'0'.repeat(64)},{bytes:receipt.bytes+1},{mediaType:'text/plain'},
    {id:'unknown.json'},{redaction:'private-owned'}]) {
    const observed=await h.invoke('test.savedCapture',{agent,captureReceipt:{...receipt,...corrupt}});
    assert.equal(observed.availability,'error');assert.equal(observed.data,undefined);
  }
  await assert.rejects(h.invoke('test.savedCapture',{agent,captureReceipt:{...receipt,redaction:'none'}}));
  assert.equal((await h.invoke('test.savedCapture',{agent:{...agent,workspaceId:'foreign'},captureReceipt:receipt})).availability,'error');
  h.wrongPin();assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  assert.equal(h.routes.length,0);
});

test('changed payload, redaction facts, snapshot boundary and missing tail cannot reuse the original capture',async t=>{
  const h=await setup(t),receipt=await h.capture();
  h.changePayload({text:'different saved answer',password:'private-capture-password'});
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  h.changePayload({text:'actual saved answer'});
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  h.setResponse(()=>({status:200,body:{events:[],snapshot_seq:2,next:0,more:false}}));
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  h.setResponse(()=>({status:200,body:{events:[],snapshot_seq:1,next:0,more:false}}));
  const missing=await h.invoke('test.savedCapture',{agent,captureReceipt:receipt});assert.equal(missing.availability,'incomplete');
  assert.equal(missing.data,undefined);
});

test('substituted captured query and owning scope cannot be adopted from safe serialized data',async t=>{
  const h=await setup(t),receipt=await h.capture();
  const wrapper=JSON.parse(await readFile(await h.store.resolve(receipt.id),'utf8'));
  for(const changed of [{...wrapper,owner:{...wrapper.owner,caseId:'foreign-case'}},
    {...wrapper,query:{...wrapper.query,after:1}},
    {...wrapper,query:{...wrapper.query,snapshotSeq:2}},
    {...wrapper,clock:{...wrapper.clock,epochUtcMs:0}},
    {...wrapper,provenance:{...wrapper.provenance,identity:{...wrapper.provenance.identity,nativeSessionId:'foreign-session'}}}]) {
    const other=await h.store.retain(JSON.stringify(changed));h.routes.length=0;
    const observed=await h.invoke('test.savedCapture',{agent,captureReceipt:other});
    assert.equal(observed.availability,'error');assert.equal(h.routes.length,0);
  }
});

test('retained artifacts reject modified bytes and hardlinks; revoked and cloned canonical contexts deny access',async t=>{
  const h=await setup(t),receipt=await h.capture(),file=await h.store.resolve(receipt.id);h.routes.length=0;
  const original=await readFile(file,'utf8');await writeFile(file,original.replace('actual saved answer','forged saved answer'));
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  await writeFile(file,original);await link(file,file+'.link');
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');
  await rm(file+'.link');
  assert.equal((await h.registry.invoke({id:'test.savedCapture',version:1,input:{}},{agent,captureReceipt:receipt},{...h.context})).availability,'error');
  revokeCapabilityContext(h.context);
  assert.equal((await h.invoke('test.savedCapture',{agent,captureReceipt:receipt})).availability,'error');assert.equal(h.routes.length,0);
});
