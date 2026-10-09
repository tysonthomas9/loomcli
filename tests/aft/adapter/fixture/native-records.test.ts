import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { NativeWorkspaceRecords } from './native-records.js';
import { AgentRow } from '../protocol.js';
import { createEvidenceStore } from '../evidence.js';
import { enrollOwnedWorkspaceAgent, requireOwnedWorkspace } from '../workspaces.js';
import type { OwnedFixture } from '../ownership.js';

const signal=()=>new AbortController().signal;
const owner={leaseId:'native-fixture',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'agents-emulator'};
async function setup(){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-records-'));
 await fs.mkdir(path.join(root,'evidence'));
 const repos=[{name:'alpha',path:path.join(root,'managed-alpha'),source_repo_id:'actual-alpha-id',groups:['common']},
  {name:'beta',path:path.join(root,'managed-beta'),source_repo_id:'actual-beta-id',groups:['common','beta-only']}];
 const body={success:true,data:{id:'E2E-AGV1-PAR',repos}};let storeGeneration='captured-store-generation',reads=0;
 let onRow:(()=>Promise<void>)|undefined;
 const row=AgentRow.parse({agent_id:'agt_beta',workspace_id:'E2E-AGV1-PAR',repo:repos[1]!.path,
  worktree_path:path.join(root,'actual-beta-worktree'),branch:'loom/agent/beta',harness:'opencode',
  harness_session_id:'ses_actual',harness_session_root:'',parent_agent_id:null,root_agent_id:null,created_by_kind:'user',
  created_by_id:'actual-user-id',preset:'lead',revision:4,state:'idle',running_turn_id:null,deleted_at:null,history_purged_at:null});
 const records=new NativeWorkspaceRecords({async store(){return {storeId:'captured-store-id',storeGeneration};},
  async read(workspaceId,view){assert.equal(workspaceId,body.data.id);assert.equal(view,'workspace');return {status:200,body};},
  async commonDir(repo){const index=repos.findIndex(value=>value.path===repo);return index>=0?path.join(root,`${repos[index]!.name}-source`,'.git'):path.join(repo,'.git');}
 },async(workspaceId,agentId)=>{reads++;assert.equal(workspaceId,body.data.id);assert.equal(agentId,'agt_beta');await onRow?.();return row;});
 await records.captureCreated(body.data.id,[repos[0]!.path,repos[1]!.path],{status:201,body},
  {storeId:'captured-store-id',storeGeneration},signal());
 return {root,repos,body,row,records,reads:()=>reads,setGeneration:(value:string)=>{storeGeneration=value;},
  onRow:(callback:()=>Promise<void>)=>{onRow=callback;},remove:()=>fs.rm(root,{recursive:true,force:true})};
}

test('native producer retains actual immutable ID, beta source and nonnull user creator through canonical enrollment',async()=>{
 const s=await setup();try{
  const store=await createEvidenceStore(path.join(s.root,'evidence')),roster=await s.records.roster(owner,store,signal());
  assert.equal(roster[0]?.identityKind,'native-agent-id');assert.equal(roster[0]?.repo,s.repos[0]!.path);
  const fixture:OwnedFixture={...owner,workspaceId:s.body.data.id,repo:roster[0]!.repo,ownedWorkspaces:roster,expiresAtUtcMs:Number.MAX_SAFE_INTEGER,
   evidenceClass:'deterministic',roots:new Map(),agents:new Map(),secrets:[],readApi:async()=>{throw Error('unused');},
   readFiles:async()=>{throw Error('unused');},resolveAgent:async()=>{throw Error('unused');},verify:async()=>{},dispose:async()=>{},
   readWorkspaceAgent:(ws,id,abort)=>s.records.nativeAgent(owner,ws,id,abort)};
  await enrollOwnedWorkspaceAgent(fixture,s.body.data.id,'agt_beta',signal(),store);
  const fact=await s.records.nativeAgent(owner,s.body.data.id,'agt_beta',signal());
  assert.equal(fact.repo,s.repos[1]!.path);assert.equal(fact.createdById,'actual-user-id');assert.equal(fact.revision,4);
  assert.equal(requireOwnedWorkspace(fixture,s.body.data.id,'agt_beta').repo,s.repos[1]!.path);
  s.body.data.repos.reverse();assert.deepEqual(await s.records.nativeAgent(owner,s.body.data.id,'agt_beta',signal()),fact);
  await assert.rejects(s.records.legacyAgent(owner,s.body.data.id,'agt_beta',signal()));
 }finally{await s.remove();}
});

test('foreign or missing workspace topology cannot enter the private native row reader',async()=>{
 const s=await setup();try{
  await assert.rejects(s.records.nativeAgent(owner,'foreign','agt_beta',signal()));assert.equal(s.reads(),0);
  s.repos[1]!.source_repo_id='foreign-source-id';await assert.rejects(s.records.nativeAgent(owner,s.body.data.id,'agt_beta',signal()));
  assert.equal(s.reads(),0);
 }finally{await s.remove();}
});

test('native row source is exact rather than alphabetical or legacy name matching',async()=>{
 for(const change of [{agent_id:'nova'},{workspace_id:'foreign'},{repo:'/foreign/owned-looking-repo'},{created_by_kind:'unknown'}]){
  const s=await setup();try{Object.assign(s.row,change);await assert.rejects(s.records.nativeAgent(owner,s.body.data.id,'agt_beta',signal()));
  }finally{await s.remove();}
 }
});

test('store and topology replacement during the owning row read rejects rather than yielding absent or zero facts',async()=>{
 for(const change of ['store','topology']){
  const s=await setup();try{s.onRow(async()=>{if(change==='store')s.setGeneration('replacement-store');else s.repos[1]!.groups.push('foreign-group');});
   await assert.rejects(s.records.nativeAgent(owner,s.body.data.id,'agt_beta',signal()));assert.equal(s.reads(),1);
  }finally{await s.remove();}
 }
});
