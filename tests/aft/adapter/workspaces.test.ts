import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createEvidenceStore } from './evidence.js';
import { createOwnedWorkspaceRoster, requireOwnedWorkspace, type OwnedWorkspaceRoster } from './workspaces.js';
import type { FixtureAuthorityOwner } from './authority.js';
import type { OwnedFixture } from './ownership.js';
const owner:FixtureAuthorityOwner={leaseId:'lease',runId:'run',suiteId:'suite',scope:'suite',caseId:'setup',profile:'deterministic'};
const fields={identityKind:'native-agent-id' as const,workspaceId:'workspace',repo:'/owned/source',commonDir:'/owned/source/.git',storeId:'store',storeGeneration:'generation',agentIds:['agt_primary']};
function makeFixture(ownedWorkspaces:OwnedWorkspaceRoster):OwnedFixture {
  const unused=async():Promise<never>=>{throw new Error('Unused deterministic workspace port');};
  return {...owner,workspaceId:'workspace',repo:fields.repo,ownedWorkspaces,verify:async()=>{},expiresAtUtcMs:Infinity,evidenceClass:'deterministic',
    roots:new Map(),agents:new Map(),secrets:[],readApi:unused,readFiles:unused,resolveAgent:unused,dispose:async()=>{}};
}
async function setup(t:{after(fn:()=>Promise<void>):void}) {
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-owned-workspaces-')));t.after(()=>rm(root,{recursive:true,force:true}));
  const store=await createEvidenceStore(root);
  const record=async(extra:Partial<typeof fields>={},identity:Partial<FixtureAuthorityOwner>={})=>{
    const values={...fields,...extra};const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...identity,...values}));
    return {...values,creationReceipt};
  };
  return {store,record};
}
test('creation receipts authorize exactly the finite workspace/agent/source roster',async t=>{
  const {store,record}=await setup(t);
  const roster=await createOwnedWorkspaceRoster(owner,[await record(),await record({workspaceId:'E2E-WS-AGENT',agentIds:['nova']})],store);
  const fixture={...owner,workspaceId:'workspace',repo:fields.repo,ownedWorkspaces:roster} as OwnedFixture;
  assert.equal(requireOwnedWorkspace(fixture,'E2E-WS-AGENT','nova').commonDir,fields.commonDir);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','agt_primary').repo,fields.repo);
  let effects=0;
  const effect=(workspace:string,agent:string)=>{requireOwnedWorkspace(fixture,workspace,agent);effects++;};
  for(const [workspace,agent] of [['unowned','nova'],['workspace','nova'],['E2E-WS-AGENT','unknown']])assert.throws(()=>effect(workspace!,agent!));
  assert.equal(effects,0);
  for(const mismatch of [{runId:'foreign'},{leaseId:'foreign'},{caseId:'foreign'},{suiteId:'foreign'},{profile:'foreign'}])
    assert.throws(()=>requireOwnedWorkspace({...fixture,...mismatch},'E2E-WS-AGENT','nova'));
  assert.throws(()=>requireOwnedWorkspace({...fixture,ownedWorkspaces:[...roster]},'workspace','agt_primary'));
  assert.ok(Object.isFrozen(roster)&&Object.isFrozen(roster[0]!.agentIds));
});
test('missing, duplicate, foreign, altered, listing-only and unreadable creation receipts are rejected',async t=>{
  const {store,record}=await setup(t);const first=await record();
  await assert.rejects(createOwnedWorkspaceRoster(owner,[],store));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[first,first],store));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[first,await record({workspaceId:'other'})],store));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[await record({},{runId:'foreign'})],store));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...first,repo:'/foreign/source'}],store));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...first,creationReceipt:{...first.creationReceipt,id:'missing'}}],store));
  const listing=await store.retain(JSON.stringify({kind:'workspace-listed',...owner,...fields}));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...first,creationReceipt:listing}],store));
  await writeFile(await store.resolve(first.creationReceipt.id),'{}');
  await assert.rejects(createOwnedWorkspaceRoster(owner,[first],store));
});

test('later product-created actors enroll through exact owned store and stale authority cannot return',async t=>{
  const {enrollOwnedWorkspaceAgent}=await import('./workspaces.js');
  const {store,record}=await setup(t);const initial=await createOwnedWorkspaceRoster(owner,[await record(),await record({workspaceId:'E2E-WS-AGENT',agentIds:[]})],store);
  const fixture=makeFixture(initial);
  let wrong=false;let reads=0;
  fixture.readWorkspaceAgent=async(workspaceId,agentId)=>{reads++;return {kind:'agent-enrolled',identityKind:'native-agent-id',...owner,workspaceId,agentId,repo:fields.repo,
    commonDir:fields.commonDir,storeId:wrong?'foreign-store':fields.storeId,storeGeneration:fields.storeGeneration,parentAgentId:null,rootAgentId:null,
    createdByKind:'user',createdById:'observed-user',revision:1};};
  assert.throws(()=>requireOwnedWorkspace(fixture,'E2E-WS-AGENT','nova'));
  await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'foreign','nova',new AbortController().signal,store));assert.equal(reads,0);
  wrong=true;await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'E2E-WS-AGENT','nova',new AbortController().signal,store));
  wrong=false;await enrollOwnedWorkspaceAgent(fixture,'E2E-WS-AGENT','nova',new AbortController().signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'E2E-WS-AGENT','nova').repo,fields.repo);
  const next=fixture.ownedWorkspaces!;assert.equal(next[1]!.enrollmentReceipts.length,1);
  const before=reads;await enrollOwnedWorkspaceAgent(fixture,'E2E-WS-AGENT','nova',new AbortController().signal,store);assert.equal(reads,before);
  fixture.ownedWorkspaces=initial;assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','agt_primary'),/stale/);
  fixture.ownedWorkspaces=undefined;assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','agt_primary'),/removed/);
  fixture.ownedWorkspaces=next;
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...next[1]!,agentIds:['nova'],enrollmentReceipts:[]}],store));
  const receipt=next[1]!.enrollmentReceipts[0]!;await writeFile(await store.resolve(receipt.id),'{}');
  await assert.rejects(createOwnedWorkspaceRoster(owner,next.map(value=>({...value,agentIds:[...value.agentIds],enrollmentReceipts:[...value.enrollmentReceipts]})),store));
});
test('later child enrollment checks actual parent/root lineage and cross-store same-name records',async t=>{
  const {enrollOwnedWorkspaceAgent}=await import('./workspaces.js');const {store,record}=await setup(t);
  const ownedWorkspaces=await createOwnedWorkspaceRoster(owner,[await record()],store);
  const fixture=makeFixture(ownedWorkspaces);
  let corrupt='';
  fixture.readWorkspaceAgent=async(workspaceId,agentId)=>({kind:'agent-enrolled',identityKind:'native-agent-id',...owner,workspaceId,agentId,repo:corrupt==='source'?'/foreign':fields.repo,
    commonDir:fields.commonDir,storeId:fields.storeId,storeGeneration:corrupt==='generation'?'foreign':fields.storeGeneration,
    parentAgentId:agentId==='agt_primary'?null:'agt_primary',rootAgentId:agentId==='agt_primary'?null:corrupt==='root'?'foreign':'agt_primary',
    createdByKind:agentId==='agt_primary'?'user':'agent',createdById:agentId==='agt_primary'?null:'agt_primary',revision:1});
  for(corrupt of ['source','generation','root'])await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'workspace','agt_child',new AbortController().signal,store));
  corrupt='';await enrollOwnedWorkspaceAgent(fixture,'workspace','agt_child',new AbortController().signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','agt_child').workspaceId,'workspace');
});

test('legacy names and native IDs have separate store authority even with identical spelling',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store,record}=await setup(t);
  const legacyRecord=async(workspaceId:string,agentIds:string[])=>{
    const values={...fields,identityKind:'legacy-agent-name' as const,workspaceId,agentIds,storeId:'legacy-store'};
    return {...values,creationReceipt:await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}))};
  };
  const roster=await createOwnedWorkspaceRoster(owner,[await record(),await legacyRecord('workspace',['nova']),await legacyRecord('other',[])],store);
  const f=makeFixture(roster);let wrong=false;let reads=0;
  f.readWorkspaceLegacyAgent=async(workspaceId,name)=>{reads++;return {kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo:fields.repo,commonDir:fields.commonDir,storeId:wrong?'native-store':'legacy-store',storeGeneration:fields.storeGeneration,parentName:null,
    createdAt:'2026-10-09T00:00:00Z',updatedAt:'2026-10-09T00:01:00Z'};};
  assert.equal(requireOwnedWorkspace(f,'workspace','nova','legacy-agent-name').repo,fields.repo);
  assert.throws(()=>requireOwnedWorkspace(f,'workspace','nova'));
  assert.throws(()=>requireOwnedWorkspace(f,'workspace','agt_primary','legacy-agent-name'));
  assert.throws(()=>requireOwnedWorkspace(f,'other','nova','legacy-agent-name'));
  wrong=true;await assert.rejects(enrollOwnedLegacyAgent(f,'other','nova',new AbortController().signal,store));
  wrong=false;await enrollOwnedLegacyAgent(f,'other','nova',new AbortController().signal,store);
  assert.equal(requireOwnedWorkspace(f,'other','nova','legacy-agent-name').workspaceId,'other');
  assert.throws(()=>requireOwnedWorkspace(f,'other','nova'));
  const before=reads;await assert.rejects(enrollOwnedLegacyAgent(f,'unowned','nova',new AbortController().signal,store));assert.equal(reads,before);
  const nativePort=f.readWorkspaceAgent;
  f.readWorkspaceLegacyAgent=async()=>{throw new Error('Legacy lineage/store observation unavailable');};
  await assert.rejects(enrollOwnedLegacyAgent(f,'other','new',new AbortController().signal,store));assert.equal(f.readWorkspaceAgent,nativePort);
});
test('legacy lineage uses actual scoped Parent observations without synthesized native actor fields',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store,record}=await setup(t);
  const values={...fields,identityKind:'legacy-agent-name' as const,workspaceId:'legacy',agentIds:['lead'],storeId:'legacy-store'};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}));
  const f=makeFixture(await createOwnedWorkspaceRoster(owner,[await record(),{...values,creationReceipt}],store));
  let parent='lead';let parentParent:string|null=null;
  f.readWorkspaceLegacyAgent=async(workspaceId,name)=>({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo:fields.repo,commonDir:fields.commonDir,storeId:'legacy-store',storeGeneration:fields.storeGeneration,parentName:name==='worker'?parent:parentParent,
    createdAt:'2026-10-09T00:00:00Z',updatedAt:'2026-10-09T00:01:00Z'});
  parent='foreign';await assert.rejects(enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store));
  parent='lead';parentParent='worker';await assert.rejects(enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store));
  parentParent=null;await enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store);
  assert.equal(requireOwnedWorkspace(f,'legacy','worker','legacy-agent-name').workspaceId,'legacy');
});

test('missing serialized identity kind fails before receipt lookup and cannot grant native authority',async t=>{
  const {WorkspaceCreationFact,WorkspaceAgentFact}=await import('./workspaces.js');const {store,record}=await setup(t);
  const first=await record();const missing={...first};Reflect.deleteProperty(missing,'identityKind');let reads=0;
  await assert.rejects(createOwnedWorkspaceRoster(owner,[missing],{retain:store.retain,resolve:async id=>{reads++;return store.resolve(id);}}));
  assert.equal(reads,0);
  const withoutKind={kind:'workspace-created',...owner,...fields};Reflect.deleteProperty(withoutKind,'identityKind');
  assert.equal(WorkspaceCreationFact.safeParse(withoutKind).success,false);
  const creationReceipt=await store.retain(JSON.stringify(withoutKind));
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...first,creationReceipt}],store));
  assert.equal(WorkspaceAgentFact.safeParse({kind:'agent-enrolled',...owner,workspaceId:'workspace',agentId:'agt_new',repo:fields.repo,
    commonDir:fields.commonDir,storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentAgentId:null,rootAgentId:null,
    createdByKind:'user',createdById:'observed-user',revision:1}).success,false);
});
