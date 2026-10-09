import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HostWorkspaceRecords, type WorkspaceStoreIdentity } from './workspace-records.js';
const signal=new AbortController().signal;
function setup(){
 let store:WorkspaceStoreIdentity={storeId:'owned-store',storeGeneration:'actual-start'},list:unknown={success:true,total:1,data:[{
  workspace_key:'OWNED',name:'nova',parent:'iris',created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'}]};
 let foreignSource=false,foreignRead=false,replaceDuringRead=false,reads=0;
 const record=new HostWorkspaceRecords({store:async()=>store,commonDir:async repo=>repo==='/source'&&foreignSource?'/foreign/.git':'/owned/.git',
  read:async(_ws,view)=>{reads++;if(replaceDuringRead)store={...store,storeGeneration:'replacement'};
   return {status:200,body:view==='workspace'?{success:true,data:{id:foreignRead?'FOREIGN':'OWNED',repos:[{path:'/owned/repo'}]}}:list};}});
 const created={status:201,body:{success:true,data:{id:'OWNED',repos:[{path:'/owned/repo'}]}}};
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
 const valid={workspace_key:'OWNED',name:'nova',created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'};
 for(const body of [{success:true,total:2,data:[valid]},{success:true,total:1,data:[{...valid,workspace_key:'FOREIGN'}]},
  {success:true,total:2,data:[valid,valid]},{success:true,total:1,data:[{...valid,created_at:undefined}]},{success:true,total:0,data:[]}]){
  r.list(body);await assert.rejects(r.record.legacyAgent(owner,'OWNED','nova',signal));
 }
 r.replace();await assert.rejects(r.record.legacyAgent(owner,'OWNED','nova',signal));
});
