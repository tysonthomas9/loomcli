import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, rm, writeFile, readFile } from 'node:fs/promises';
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
  const values={...fields,identityKind:'legacy-agent-name' as const,workspaceId:'legacy',agentIds:[],storeId:'legacy-store'};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}));
  const f=makeFixture(await createOwnedWorkspaceRoster(owner,[await record(),{...values,creationReceipt}],store));
  let parent='lead';let parentParent:string|null=null;
  f.readWorkspaceLegacyAgent=async(workspaceId,name)=>({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo:fields.repo,commonDir:fields.commonDir,storeId:'legacy-store',storeGeneration:fields.storeGeneration,parentName:name==='worker'?parent:parentParent,
    createdAt:'2026-10-09T00:00:00Z',updatedAt:'2026-10-09T00:01:00Z'});
  await enrollOwnedLegacyAgent(f,'legacy','lead',new AbortController().signal,store);
  parent='foreign';await assert.rejects(enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store));
  parent='lead';parentParent='worker';await assert.rejects(enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store));
  parentParent=null;await enrollOwnedLegacyAgent(f,'legacy','worker',new AbortController().signal,store);
  assert.equal(requireOwnedWorkspace(f,'legacy','worker','legacy-agent-name').workspaceId,'legacy');
});

test('existing legacy membership revalidates retained creation identity without freezing mutable updates',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store}=await setup(t),signal=new AbortController().signal;
  const values={...fields,identityKind:'legacy-agent-name' as const,agentIds:[],repositories,agentSources:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...values,creationReceipt}],store));
  let createdAt='actual-creation',updatedAt='first-update',parentName:string|null=null,assignedRepos=['alpha','beta'];
  let reads=0,missing=false;
  fixture.readWorkspaceLegacyAgent=async(workspaceId,name)=>{
    reads++;if(missing)throw new Error('Actual actor row is absent');
    return {kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,repo:null,commonDir:null,
      storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName,createdAt,updatedAt,assignedRepos,assignedRepoGroups:[]};
  };
  await enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store);
  const roster=fixture.ownedWorkspaces!,receiptCount=roster[0]!.enrollmentReceipts.length;
  updatedAt='normal-mutable-update';assignedRepos=['beta'];
  const before=reads;await enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store);
  assert.ok(reads>before);assert.equal(fixture.ownedWorkspaces,roster);assert.equal(roster[0]!.enrollmentReceipts.length,receiptCount);
  createdAt='replacement-creation';await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store),/creation identity/);
  createdAt='actual-creation';parentName='foreign';await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store));
  parentName=null;missing=true;await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store));missing=false;
  const retained=roster[0]!.enrollmentReceipts[0]!;await writeFile(await store.resolve(retained.id),'{}');
  await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store));
});

test('existing legacy lineage rejects replaced parent, cycles and identity changes during read',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store}=await setup(t),signal=new AbortController().signal;
  const values={...fields,identityKind:'legacy-agent-name' as const,agentIds:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...values,creationReceipt}],store));
  let parentCreated='lead-created',workerCreated='worker-created',leadParent:string|null=null,changeOnSecond=false,workerReads=0;
  fixture.readWorkspaceLegacyAgent=async(workspaceId,name)=>{
    if(name==='worker'){workerReads++;if(changeOnSecond&&workerReads===2)workerCreated='raced-replacement';}
    return {kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,repo:fields.repo,commonDir:fields.commonDir,
      storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName:name==='worker'?'lead':leadParent,
      createdAt:name==='worker'?workerCreated:parentCreated,updatedAt:'mutable-update'};
  };
  await enrollOwnedLegacyAgent(fixture,'workspace','lead',signal,store);
  await enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store);
  parentCreated='recreated-lead';await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store),/creation identity/);
  parentCreated='lead-created';leadParent='worker';await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store));
  leadParent=null;workerReads=0;changeOnSecond=true;
  await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','worker',signal,store),/creation identity/);
  assert.deepEqual(fixture.ownedWorkspaces![0]!.agentIds,['lead','worker']);
});

test('creation membership without an immutable legacy enrollment receipt cannot adopt a current row',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store,record}=await setup(t),signal=new AbortController().signal;
  const values={...fields,identityKind:'legacy-agent-name' as const,workspaceId:'legacy',agentIds:['worker']};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...values}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[await record(),{...values,creationReceipt}],store));
  fixture.readWorkspaceLegacyAgent=async()=>{throw new Error('A current listing cannot establish original creation');};
  await assert.rejects(enrollOwnedLegacyAgent(fixture,'legacy','worker',signal,store),/retained creation identity/);
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

const repositories=[{repoName:'alpha',sourceRepoId:'source-alpha',repo:fields.repo,commonDir:fields.commonDir,groups:['shared']},
  {repoName:'beta',sourceRepoId:'source-beta',repo:'/owned/beta',commonDir:'/owned/beta/.git',groups:['beta-only','shared']}];
test('canonical legacy assignment helper retains source union, order and missing observation rejection',async()=>{
  const {resolveLegacyRepositoryAssignments:resolve}=await import('./workspaces.js');
  assert.deepEqual(resolve(repositories,[],[]),['alpha','beta']);
  assert.deepEqual(resolve([...repositories].reverse(),[],[]),['beta','alpha']);
  assert.deepEqual(resolve(repositories,[],['beta-only']),['beta']);
  assert.deepEqual(resolve(repositories,['alpha'],['shared']),['alpha','beta']);
  assert.deepEqual(resolve(repositories,['alpha'],['unknown']),['alpha']);
  assert.ok(Object.isFrozen(resolve(repositories,[],[])));
  for(const [names,groups] of [[undefined,[]],[[],undefined],[[],['unknown']],[['foreign'],[]],[['alpha','alpha'],[]],[[],['shared','shared']]] as const)
    assert.throws(()=>resolve(repositories,names,groups));
});
test('legacy unselected physical source retains actual multi-repository membership without an anchor choice',async t=>{
  const {LegacyWorkspaceAgentFact,enrollOwnedLegacyAgent,requireOwnedWorkspaceRecord}=await import('./workspaces.js');
  const {store}=await setup(t),signal=new AbortController().signal;
  for(const named of [repositories,[...repositories].reverse()]) {
    const initial={...fields,identityKind:'legacy-agent-name' as const,agentIds:[],repositories:named,agentSources:[]};
    const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
    const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store));
    const fact=LegacyWorkspaceAgentFact.parse({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId:'workspace',name:'multi',
      repo:null,commonDir:null,storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName:null,
      createdAt:'actual-created',updatedAt:'actual-updated',assignedRepos:['alpha','beta'],assignedRepoGroups:[]});
    fixture.readWorkspaceLegacyAgent=async()=>fact;
    await enrollOwnedLegacyAgent(fixture,'workspace','multi',signal,store);
    assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','multi','legacy-agent-name'),/ambiguous/);
    const topology=requireOwnedWorkspaceRecord(fixture,'workspace','legacy-agent-name');assert.ok(topology);assert.equal(topology.repo,fields.repo);
    assert.deepEqual(topology.agentSources,[{agentId:'multi',repoNames:['alpha','beta']}]);
    assert.equal(requireOwnedWorkspace(fixture,'workspace','multi','legacy-agent-name','beta').commonDir,'/owned/beta/.git');
    const retained=JSON.parse(await readFile(await store.resolve(topology.enrollmentReceipts[0]!.id),'utf8'));
    assert.equal(retained.repo,null);assert.equal(retained.commonDir,null);
    for(const changed of [{...fact,repo:fields.repo},{...fact,commonDir:fields.commonDir},
      {...fact,repo:undefined},{...fact,commonDir:undefined}])assert.equal(LegacyWorkspaceAgentFact.safeParse(changed).success,false);
  }
});

test('unselected legacy source cannot bypass assigned groups, foreign or stale topology and single-source receipts',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store}=await setup(t),signal=new AbortController().signal;
  const initial={...fields,identityKind:'legacy-agent-name' as const,agentIds:[],repositories,agentSources:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store));
  let groups=['beta-only'];let assigned:string[]=[];let selected:string|null=null;
  fixture.readWorkspaceLegacyAgent=async(workspaceId,name)=>({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo:selected,commonDir:selected===null?null:selected+'/.git',storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName:null,
    createdAt:'actual-created',updatedAt:'actual-updated',assignedRepos:assigned,assignedRepoGroups:groups});
  await enrollOwnedLegacyAgent(fixture,'workspace','grouped-null',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','grouped-null','legacy-agent-name').repo,'/owned/beta');
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','grouped-null','legacy-agent-name','alpha'));
  groups=['unknown'];await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','missing-group',signal,store));
  groups=[];assigned=['foreign'];await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','foreign',signal,store));
  assigned=['beta'];selected=fields.repo;await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','anchor',signal,store));
  selected='/owned/beta';await enrollOwnedLegacyAgent(fixture,'workspace','actual-beta',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','actual-beta','legacy-agent-name').repo,'/owned/beta');
  const current=fixture.ownedWorkspaces!;fixture.ownedWorkspaces=await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store);
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','actual-beta','legacy-agent-name'),/stale/);fixture.ownedWorkspaces=current;
  const singleFields={...fields,identityKind:'legacy-agent-name' as const,agentIds:[]};
  const singleReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...singleFields}));
  const single=makeFixture(await createOwnedWorkspaceRoster(owner,[{...singleFields,creationReceipt:singleReceipt}],store));
  single.readWorkspaceLegacyAgent=fixture.readWorkspaceLegacyAgent;selected=null;assigned=[];
  await assert.rejects(enrollOwnedLegacyAgent(single,'workspace','unselected-single',signal,store));
});
test('finite named repositories resolve actual native actors without a primary or position alias',async t=>{
  const {enrollOwnedWorkspaceAgent,requireOwnedWorkspaceRecord}=await import('./workspaces.js');const {store}=await setup(t);
  const initial={...fields,agentIds:[],repositories,agentSources:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  const roster=await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store),fixture=makeFixture(roster);
  assert.equal(requireOwnedWorkspaceRecord(fixture,'workspace')!.repositories!.length,2);
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace'),/ambiguous/);
  assert.equal(requireOwnedWorkspace(fixture,'workspace',undefined,'native-agent-id','beta').repo,'/owned/beta');
  fixture.readWorkspaceAgent=async(workspaceId,agentId)=>({kind:'agent-enrolled',identityKind:'native-agent-id',...owner,workspaceId,agentId,
    repo:'/owned/beta',commonDir:'/owned/beta/.git',storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentAgentId:null,rootAgentId:null,
    createdByKind:'user',createdById:'actual-user',revision:1});
  await enrollOwnedWorkspaceAgent(fixture,'workspace','agt_later',new AbortController().signal,store);
  const selected=requireOwnedWorkspace(fixture,'workspace','agt_later');assert.equal(selected.repoName,'beta');assert.equal(selected.sourceRepoId,'source-beta');
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','agt_later','native-agent-id','alpha'));
  assert.deepEqual(fixture.ownedWorkspaces![0]!.agentSources,[{agentId:'agt_later',repoNames:['beta']}]);
  assert.ok(Object.isFrozen(fixture.ownedWorkspaces![0]!.repositories)&&Object.isFrozen(fixture.ownedWorkspaces![0]!.agentSources![0]!.repoNames));
  let effects=0;const effect=()=>{requireOwnedWorkspace(fixture,'workspace','agt_later','native-agent-id','alpha');effects++;};assert.throws(effect);assert.equal(effects,0);
});
test('repository ambiguity, changed source, missing associations and same actor cross-repo deny ownership',async t=>{
  const {store}=await setup(t);const initial={...fields,repositories,agentSources:[{agentId:'agt_primary',repoNames:['alpha']}]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  for(const change of [
    {...initial,repositories:[repositories[0]!,{...repositories[1]!,repoName:'alpha'}]},
    {...initial,repositories:[repositories[0]!,{...repositories[1]!,repo:fields.repo}]},
    {...initial,repositories:[repositories[0]!,{...repositories[1]!,commonDir:'/foreign/.git'}]},
    {...initial,repositories:[repositories[0]!,{...repositories[1]!,groups:['changed']}]},
    {...initial,agentSources:undefined},
    {...initial,agentSources:[{agentId:'agt_primary',repoNames:['foreign']}]},
    {...initial,agentSources:[{agentId:'agt_primary',repoNames:['alpha','beta']}]},
    {...initial,agentIds:['agt_primary','agt_primary'],agentSources:[{agentId:'agt_primary',repoNames:['alpha']},{agentId:'agt_primary',repoNames:['beta']}]},
  ])await assert.rejects(createOwnedWorkspaceRoster(owner,[{...change,creationReceipt}],store));
});
test('legacy actual empty Repos means the finite owned set and explicit source selection is required',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store}=await setup(t);
  const initial={...fields,identityKind:'legacy-agent-name' as const,agentIds:[],repositories,agentSources:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store));
  let assignment:string[]|undefined;
  fixture.readWorkspaceLegacyAgent=async(workspaceId,name)=>({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo:fields.repo,commonDir:fields.commonDir,storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName:null,
    createdAt:'actual-created',updatedAt:'actual-updated',assignedRepos:assignment,assignedRepoGroups:[]});
  await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','operator',new AbortController().signal,store));
  assignment=['foreign'];await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','operator',new AbortController().signal,store));
  assignment=[];await enrollOwnedLegacyAgent(fixture,'workspace','operator',new AbortController().signal,store);
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','operator','legacy-agent-name'),/ambiguous/);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','operator','legacy-agent-name','beta').repo,'/owned/beta');
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','operator','native-agent-id','beta'));
});
test('created workspace append authenticates receipts and cannot restore a stale or foreign roster',async t=>{
  const {appendCreatedWorkspaces}=await import('./workspaces.js');const {store,record}=await setup(t);
  const prior=await createOwnedWorkspaceRoster(owner,[await record()],store),fixture=makeFixture(prior),signal=new AbortController().signal;
  await appendCreatedWorkspaces(fixture,[await record({workspaceId:'later',agentIds:['agt_new']})],signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'later','agt_new').repo,fields.repo);
  const next=fixture.ownedWorkspaces!;
  await assert.rejects(appendCreatedWorkspaces(fixture,[await record({workspaceId:'foreign',agentIds:[]},{runId:'foreign'})],signal,store));
  assert.equal(fixture.ownedWorkspaces,next);
  await assert.rejects(appendCreatedWorkspaces(fixture,[await record({workspaceId:'later',agentIds:[]})],signal,store));
  fixture.ownedWorkspaces=prior;assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','agt_primary'),/stale/);
});

test('legacy groups restrict actual sources and never turn empty explicit Repos into all-repo authority',async t=>{
  const {enrollOwnedLegacyAgent}=await import('./workspaces.js');const {store}=await setup(t);
  const initial={...fields,identityKind:'legacy-agent-name' as const,agentIds:[],repositories,agentSources:[]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store));
  let groups:string[]|undefined;let repo='/owned/beta';let assignedRepos:string[]=[];
  fixture.readWorkspaceLegacyAgent=async(workspaceId,name)=>({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...owner,workspaceId,name,
    repo,commonDir:repo===fields.repo?fields.commonDir:'/owned/beta/.git',storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentName:null,
    createdAt:'actual-created',updatedAt:'actual-updated',assignedRepos,assignedRepoGroups:groups});
  const signal=new AbortController().signal;
  await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','grouped',signal,store));
  groups=['unknown'];await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','grouped',signal,store));
  groups=['beta-only'];repo=fields.repo;await assert.rejects(enrollOwnedLegacyAgent(fixture,'workspace','grouped',signal,store));
  repo='/owned/beta';await enrollOwnedLegacyAgent(fixture,'workspace','grouped',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','grouped','legacy-agent-name').repo,'/owned/beta');
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','grouped','legacy-agent-name','alpha'));
  assignedRepos=['alpha'];groups=['shared'];await enrollOwnedLegacyAgent(fixture,'workspace','union',signal,store);
  assert.deepEqual(fixture.ownedWorkspaces![0]!.agentSources!.find(value=>value.agentId==='union')!.repoNames,['alpha','beta']);
  groups=[];repo=fields.repo;await enrollOwnedLegacyAgent(fixture,'workspace','named-only',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','named-only','legacy-agent-name').repoName,'alpha');
  assert.throws(()=>requireOwnedWorkspace(fixture,'workspace','named-only','legacy-agent-name','beta'));
  assert.ok(Object.isFrozen(fixture.ownedWorkspaces![0]!.repositories![0]!.groups));
});

test('native child may use another owned repository while parent retains its own exact source',async t=>{
  const {enrollOwnedWorkspaceAgent}=await import('./workspaces.js');const {store}=await setup(t);
  const initial={...fields,repositories,agentSources:[{agentId:'agt_primary',repoNames:['alpha']}]};
  const creationReceipt=await store.retain(JSON.stringify({kind:'workspace-created',...owner,...initial}));
  const fixture=makeFixture(await createOwnedWorkspaceRoster(owner,[{...initial,creationReceipt}],store));
  let parentRepo=fields.repo,parentCommon=fields.commonDir,childRepo='/owned/beta',childCommon='/owned/beta/.git';
  fixture.readWorkspaceAgent=async(workspaceId,agentId)=>({kind:'agent-enrolled',identityKind:'native-agent-id',...owner,workspaceId,agentId,
    repo:agentId==='agt_primary'?parentRepo:childRepo,commonDir:agentId==='agt_primary'?parentCommon:childCommon,
    storeId:fields.storeId,storeGeneration:fields.storeGeneration,parentAgentId:agentId==='agt_primary'?null:'agt_primary',
    rootAgentId:agentId==='agt_primary'?null:'agt_primary',createdByKind:agentId==='agt_primary'?'user':'agent',createdById:agentId==='agt_primary'?'actual-user':'agt_primary',revision:1});
  const signal=new AbortController().signal;
  parentRepo='/foreign';await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'workspace','child',signal,store));
  parentRepo='/owned/beta';parentCommon='/owned/beta/.git';await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'workspace','child',signal,store));
  parentRepo=fields.repo;parentCommon=fields.commonDir;childRepo='/foreign';await assert.rejects(enrollOwnedWorkspaceAgent(fixture,'workspace','child',signal,store));
  childRepo='/owned/beta';await enrollOwnedWorkspaceAgent(fixture,'workspace','cross-repo-child',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','cross-repo-child').repo,'/owned/beta');
  childRepo=fields.repo;childCommon=fields.commonDir;await enrollOwnedWorkspaceAgent(fixture,'workspace','same-repo-child',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'workspace','same-repo-child').repo,fields.repo);
});
