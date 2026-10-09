import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HostWorkspaceRecords, type WorkspaceStoreIdentity } from './workspace-records.js';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createEvidenceStore } from '../evidence.js';
import { enrollOwnedLegacyAgent,requireOwnedWorkspace } from '../workspaces.js';
import type { OwnedFixture } from '../ownership.js';
const signal=new AbortController().signal;
function setup(){
 let store:WorkspaceStoreIdentity={storeId:'owned-store',storeGeneration:'actual-start'},list:unknown={success:true,total:1,data:[{
  workspace_key:'OWNED',name:'nova',parent:'iris',repos:[],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'}]};
 let foreignSource=false,foreignRead=false,replaceDuringRead=false,reads=0;
 const record=new HostWorkspaceRecords({store:async()=>store,commonDir:async repo=>repo==='/source'&&foreignSource?'/foreign/.git':'/owned/.git',
  read:async(_ws,view)=>{reads++;if(replaceDuringRead)store={...store,storeGeneration:'replacement'};
   return {status:200,body:view==='workspace'?{success:true,data:{id:foreignRead?'FOREIGN':'OWNED',repos:[{name:'repo',path:'/owned/repo',source_repo_id:'actual-source',groups:[]}]}}:list};}});
 const created={status:201,body:{success:true,data:{id:'OWNED',repos:[{name:'repo',path:'/owned/repo',source_repo_id:'actual-source',groups:[]}]}}};
 return {record,created,store,get reads(){return reads;},foreignSource(){foreignSource=true;},foreignRead(){foreignRead=true;},replace(){replaceDuringRead=true;},list(value:unknown){list=value;}};
}
test('creation cannot be replaced by requested IDs, discovery, failed creation or foreign Git association',async()=>{
 for(const change of ['status','source','read','generation','id'] as const){const r=setup();
  if(change==='status')r.created.status=200;if(change==='source')r.foreignSource();if(change==='read')r.foreignRead();if(change==='generation')r.replace();if(change==='id')r.created.body.data.id='FOREIGN';
  await assert.rejects(r.record.captureCreated('OWNED','/source',r.created,r.store,signal));
  const before=r.reads;await assert.rejects(r.record.legacyAgent({leaseId:'lease',runId:'run',suiteId:'suite',scope:'case',caseId:'case',profile:'legacy-deterministic'},'OWNED','nova',signal));
  assert.equal(r.reads,before);
 }
});
test('legacy facts preserve actual scoped name, parent and timestamps and reject partial, foreign or duplicate rows',async()=>{
 const r=setup();await r.record.captureCreated('OWNED','/source',r.created,r.store,signal);
 const owner={leaseId:'lease',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'legacy-deterministic'};
 const fact=await r.record.legacyAgent(owner,'OWNED','nova',signal);assert.equal(fact.parentName,'iris');assert.equal(fact.createdAt,'2026-10-09T00:00:00Z');
 const valid={workspace_key:'OWNED',name:'nova',repos:[],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'};
 for(const body of [{success:true,total:2,data:[valid]},{success:true,total:1,data:[{...valid,workspace_key:'FOREIGN'}]},
  {success:true,total:2,data:[valid,valid]},{success:true,total:1,data:[{...valid,created_at:undefined}]},{success:true,total:0,data:[]}]){
  r.list(body);await assert.rejects(r.record.legacyAgent(owner,'OWNED','nova',signal));
 }
 r.replace();await assert.rejects(r.record.legacyAgent(owner,'OWNED','nova',signal));
});

test('named multi-repository creation retains actual source IDs and groups, then enrolls actual beta assignments',async()=>{
 const directory=await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname),'test-artifacts-'));
 try{
  const owner={leaseId:'lease',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'legacy-deterministic'},identity={storeId:'actual-store',storeGeneration:'actual-generation'};
  const repos=[{name:'alpha',path:'/managed/alpha',source_repo_id:'actual-source-alpha',groups:['backend']},
   {name:'beta',path:'/managed/beta',source_repo_id:'actual-source-beta',groups:['frontend']}];
  const data={id:'OWNED',repos},agent={workspace_key:'OWNED',name:'nova',repos:['beta'],repo_groups:[] as string[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'};
  const record=new HostWorkspaceRecords({store:async()=>identity,read:async(_ws,view)=>({status:200,body:view==='workspace'?{success:true,data}:{success:true,total:1,data:[agent]}}),
   commonDir:async repo=>repo.endsWith('alpha')?'/sources/alpha/.git':repo.endsWith('beta')?'/sources/beta/.git':'/foreign/.git'});
  await record.captureCreated('OWNED',['/sources/alpha','/sources/beta'],{status:201,body:{success:true,data}},identity,signal);
  const store=await createEvidenceStore(directory),roster=await record.roster(owner,store,signal);
  assert.deepEqual(roster[0]!.repositories?.map(repo=>[repo.repoName,repo.sourceRepoId,repo.groups]),
   [['alpha','actual-source-alpha',['backend']],['beta','actual-source-beta',['frontend']]]);
  const fixture:OwnedFixture={...owner,workspaceId:'OWNED',repo:'/managed/alpha',ownedWorkspaces:roster,
   roots:new Map(),agents:new Map(),secrets:[],expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',
   verify:async()=>{await record.workspace('OWNED',signal);},dispose:async()=>{},
   readApi:async()=>{throw new Error('unused');},readFiles:async()=>{throw new Error('unused');},resolveAgent:async()=>{throw new Error('unused');},
   readWorkspaceLegacyAgent:(ws,name,abort)=>record.legacyAgent(owner,ws,name,abort)};
  await enrollOwnedLegacyAgent(fixture,'OWNED','nova',signal,store);
  const selected=requireOwnedWorkspace(fixture,'OWNED','nova','legacy-agent-name');
  assert.equal(selected.repo,'/managed/beta');assert.equal(selected.commonDir,'/sources/beta/.git');assert.equal(selected.sourceRepoId,'actual-source-beta');
  assert.throws(()=>requireOwnedWorkspace(fixture,'OWNED','nova','legacy-agent-name','alpha'));
  agent.repos=[];agent.repo_groups=['frontend'];assert.equal((await record.legacyAgent(owner,'OWNED','nova',signal)).repo,'/managed/beta');
  agent.repos=['beta'];agent.repo_groups=['unmatched-group'];assert.equal((await record.legacyAgent(owner,'OWNED','nova',signal)).repo,'/managed/beta');
  for(const assignment of [{repos:['foreign'],repo_groups:[]},{repos:[],repo_groups:['foreign']},{repos:['beta','beta'],repo_groups:[]}]){
   Object.assign(agent,assignment);await assert.rejects(record.legacyAgent(owner,'OWNED','nova',signal));
  }
 }finally{await fs.rm(directory,{recursive:true});}
});

test('multi-repository ownership rejects missing fields, duplicate physical sources and changed creation facts',async()=>{
 const base={id:'OWNED',repos:[{name:'alpha',path:'/managed/alpha',source_repo_id:'actual-alpha',groups:[]},
  {name:'beta',path:'/managed/beta',source_repo_id:'actual-beta',groups:[]}]},identity={storeId:'actual-store',storeGeneration:'actual-generation'};
 for(const mutation of ['missing-name','missing-source-id','missing-groups','duplicate-name','duplicate-path','changed-id','foreign-common','duplicate-source-common'] as const){
  const created=structuredClone(base),read=structuredClone(base);
  if(mutation==='missing-name')Object.assign(created.repos[0]!,{name:undefined});
  if(mutation==='missing-source-id')Object.assign(created.repos[0]!,{source_repo_id:undefined});
  if(mutation==='missing-groups')Object.assign(created.repos[0]!,{groups:undefined});
  if(mutation==='duplicate-name')created.repos[1]!.name='alpha';
  if(mutation==='duplicate-path')created.repos[1]!.path='/managed/alpha';
  if(mutation==='changed-id')read.repos[1]!.source_repo_id='replacement';
  const records=new HostWorkspaceRecords({store:async()=>identity,read:async()=>({status:200,body:{success:true,data:read}}),
   commonDir:async repo=>mutation==='duplicate-source-common'?'/same/.git':repo==='/managed/beta'&&mutation==='foreign-common'?'/foreign/.git':repo.endsWith('alpha')?'/alpha/.git':'/beta/.git'});
  await assert.rejects(records.captureCreated('OWNED',['/sources/alpha','/sources/beta'],{status:201,body:{success:true,data:created}},identity,signal));
  assert.equal(records.has('OWNED'),false);
 }
 const r=setup();await r.record.captureCreated('OWNED','/source',r.created,r.store,signal);
 r.list({success:true,total:1,data:[{workspace_key:'OWNED',name:'nova',created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'}]});
 await assert.rejects(r.record.legacyAgent({leaseId:'lease',runId:'run',suiteId:'suite',scope:'case',caseId:'case',profile:'legacy-deterministic'},'OWNED','nova',signal));
});

test('multi-assigned membership has no invented selected source, regardless of topology order',async()=>{
 const directory=await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname),'test-artifacts-'));
 try{
  const owner={leaseId:'lease',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'legacy-deterministic'},identity={storeId:'actual-store',storeGeneration:'actual-generation'};
  const repositories=[{name:'alpha',path:'/managed/alpha',source_repo_id:'source-alpha',groups:['shared']},
   {name:'beta',path:'/managed/beta',source_repo_id:'source-beta',groups:['shared']}];
  const store=await createEvidenceStore(directory);
  for(const reverse of [false,true])for(const assignment of [{repos:['alpha','beta'],repo_groups:[]},{repos:[],repo_groups:['shared']},{repos:[],repo_groups:[]}]){
   const data={id:'OWNED',repos:reverse?[...repositories].reverse():repositories},agent={workspace_key:'OWNED',name:'nova',...assignment,created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'};
   const record=new HostWorkspaceRecords({store:async()=>identity,
    read:async(_ws,view)=>({status:200,body:view==='workspace'?{success:true,data}:{success:true,total:1,data:[agent]}}),
    commonDir:async repo=>repo.endsWith('alpha')?'/sources/alpha/.git':'/sources/beta/.git'});
   await record.captureCreated('OWNED',['/sources/alpha','/sources/beta'],{status:201,body:{success:true,data}},identity,signal);
   const fact=await record.legacyAgent(owner,'OWNED','nova',signal);
   assert.equal(fact.repo,null);assert.equal(fact.commonDir,null);
   assert.deepEqual(fact.assignedRepos,assignment.repos);assert.deepEqual(fact.assignedRepoGroups,assignment.repo_groups);
   const fixture:OwnedFixture={...owner,workspaceId:'OWNED',repo:'/managed/alpha',
    ownedWorkspaces:await record.roster(owner,store,signal),roots:new Map(),agents:new Map(),secrets:[],expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',
    verify:async()=>{await record.workspace('OWNED',signal);},dispose:async()=>{},
    readApi:async()=>{throw new Error('unused');},readFiles:async()=>{throw new Error('unused');},resolveAgent:async()=>{throw new Error('unused');},
    readWorkspaceLegacyAgent:(ws,name,abort)=>record.legacyAgent(owner,ws,name,abort)};
   await enrollOwnedLegacyAgent(fixture,'OWNED','nova',signal,store);
   assert.throws(()=>requireOwnedWorkspace(fixture,'OWNED','nova','legacy-agent-name'));
   assert.equal(requireOwnedWorkspace(fixture,'OWNED','nova','legacy-agent-name','beta').repo,'/managed/beta');
  }
 }finally{await fs.rm(directory,{recursive:true});}
});
