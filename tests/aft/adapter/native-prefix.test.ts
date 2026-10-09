import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile, mkdtemp, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { runInNewContext } from 'node:vm';
import { DatabaseSync } from 'node:sqlite';
import { AgentRow, NativeAgentIdentity, sha256, type Json, type HttpResponse, type NativeAccess } from './protocol.js';
import { createNativeHostAccess } from './native-host.js';
import { NativeInput, observeNative, projectNativeAssistantPrefix } from './native.js';
const row=AgentRow.parse({agent_id:'agt_owned',workspace_id:'workspace',repo:'/owned/source',worktree_path:'/owned/tree',branch:'owned',
  harness:'opencode',harness_session_id:'ses_owned',harness_session_root:'',parent_agent_id:null,root_agent_id:null,
  created_by_kind:'user',created_by_id:null,preset:'lead',revision:1,state:'idle',running_turn_id:null,deleted_at:null,history_purged_at:null});
const input=NativeInput.parse({agent:{fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_owned'},view:'assistant-prefix',
  nativeSessionId:'ses_owned',nativeRoot:'',expectedGeneration:'generation',maxMessages:200});
const eligible=(id:string,model='wrong')=>({id,sessionID:'ses_owned',type:'assistant',time:{completed:1},finish:'stop',model:{providerID:'provider',id:model}});
function access(data:Json[]){
  const calls:string[]=[];let changed=false;
  const native:NativeAccess={pinnedExecutable:'/owned/opencode',async registration(){return {url:'http://127.0.0.1:4123/',pid:42,password:'private',generation:'generation',endpointId:'endpoint'};},
    async process(){return {pid:42,generation:'generation',executable:'/owned/opencode',argv:['/owned/opencode','serve','--service']};},
    async agent(){return {...row,revision:changed?2:1};},async sessions(){return [{agent_id:row.agent_id,harness:'opencode',native_id:'ses_owned',native_root:''}];},
    async read(route,signal):Promise<HttpResponse>{signal.throwIfAborted();calls.push(route);return {status:200,body:route==='/api/info'?{pid:42}:route.includes('/message?')?{data}:
      {data:{id:'ses_owned',metadata:{agent_id:'agt_owned'},location:{directory:'/owned/tree'},model:{variant:'source-effort'}}}};}};
  return {native,calls,change(){changed=true;}};
}
test('prefix follows the hash-bound original filter including first/order/truthiness',async()=>{
  const source=await readFile(new URL('../scripts/coverage-chat-controls-stop-native.sh',import.meta.url),'utf8');
  assert.equal(await sha256(source),'f46ea6dbb4c270269f541abd0d13f48cb5129f71985b98ba48c74d3dfda815e9');
  const literal=source.match(/const completed = (messages\.data\.filter\([^\n]+\));/)?.[1];assert.ok(literal);
  const data:Json[]=[null,{}, {type:'assistant',time:{completed:0},finish:'stop'}, {type:'assistant',time:{completed:''},finish:'stop'},
    {type:'assistant',time:{completed:1},finish:0},{type:'assistant',time:{completed:1},finish:'stop',error:{}},eligible('first','wrong'),eligible('later','target'),
    {type:'assistant',time:{completed:'truthy'},finish:{actual:true},error:false}];
  for(let rotation=0;rotation<data.length;rotation++){
    const rows=[...data.slice(rotation),...data.slice(0,rotation)],expected=runInNewContext(literal,{messages:{data:rows}}) as Json[];
    const actual=projectNativeAssistantPrefix({data:rows});
    assert.deepEqual(actual.eligibleIndices,Array.from(expected,message=>rows.indexOf(message)));
    assert.deepEqual(actual.selected,expected.length?{index:rows.indexOf(expected[0]!),message:expected[0]}:null);
  }
  assert.equal(projectNativeAssistantPrefix({data}).selected?.index,6);
  const selectedModel='const completed = '+literal+'; const model = completed[0]?.model; model?.providerID && model?.id ? model.providerID + \"/\" + model.id : null';
  for(const model of [{providerID:'provider',id:'target'},{providerID:0,id:'target'},{providerID:1,id:true},{providerID:['a','b'],id:{literal:true}},{providerID:'first',id:'wrong'},null]){
    const messages=[{...eligible('actual'),model},eligible('later','target')];
    assert.equal(projectNativeAssistantPrefix({data:messages}).observedModel,runInNewContext(selectedModel,{messages:{data:messages}}));
  }
});
test('full 200 prefix is valid without complete-history claims, malformed/overflow deny',()=>{
  const rows=Array.from({length:200},(_,i)=>eligible(String(i))),actual=projectNativeAssistantPrefix({data:rows});
  assert.equal(actual.returnedCount,200);assert.equal(actual.selected?.index,0);assert.equal(Object.hasOwn(actual,'complete'),false);
  assert.equal(projectNativeAssistantPrefix({data:[]}).selected,null);
  assert.throws(()=>projectNativeAssistantPrefix({data:[...rows,eligible('extra')]}));assert.throws(()=>projectNativeAssistantPrefix({data:{}}));
});
test('native view fixes assistant desc200, preserves original effort and old full-page semantics',async()=>{
  const h=access(Array.from({length:200},(_,i)=>eligible(String(i)))),actual=await observeNative(input,h.native,row,new AbortController().signal);
  assert.equal(actual.view,'assistant-prefix');if(actual.view!=='assistant-prefix')throw Error('wrong view');
  assert.equal(actual.prefix.selected?.index,0);assert.equal(actual.nativeEffort,'source-effort');
  assert.ok(h.calls.includes('/api/session/ses_owned/message?type=assistant&order=desc&limit=200'));
  h.native.read=async (route):Promise<HttpResponse>=>({status:200,body:route==='/api/info'?{pid:42}:route.includes('/message?')?{data:Array.from({length:200},(_,i)=>eligible(String(i)))}:
    {data:{id:'ses_owned',metadata:{agent_id:'agt_owned'},location:{directory:'/owned/tree'}}}});
  const old=await observeNative({...input,view:'completed-models'},h.native,row,new AbortController().signal);
  assert.equal(old.view,'completed-models');assert.ok('complete'in old);assert.equal(old.complete,false);
});
test('fixed prefix bound is pre-read; foreign message and post-read replacement deny',async()=>{
  const h=access([eligible('message')]);await assert.rejects(observeNative({...input,maxMessages:199},h.native,row,new AbortController().signal));assert.deepEqual(h.calls,[]);
  const foreign=access([{...eligible('foreign'),sessionID:'foreign'}]);await assert.rejects(observeNative(input,foreign.native,row,new AbortController().signal),/foreign message/);
  const original=h.native.read;h.native.read=async(route,signal)=>{const value=await original(route,signal);if(route.includes('/message?'))h.change();return value;};
  await assert.rejects(observeNative(input,h.native,row,new AbortController().signal),/changed/);
});
test('actual identity SQL requires name/created_at and preserves old row consumers',async t=>{
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-native-identity-')));t.after(()=>rm(root,{recursive:true,force:true}));
  const db=new DatabaseSync(path.join(root,'agents.db'));t.after(()=>db.close());
  db.exec(`CREATE TABLE agents(agent_id TEXT,workspace_id TEXT,name TEXT,created_at TEXT,repo TEXT,worktree_path TEXT,branch TEXT,harness TEXT,
    harness_session_id TEXT,harness_session_root TEXT,parent_agent_id TEXT,root_agent_id TEXT,created_by_kind TEXT,created_by_id TEXT,
    preset TEXT,revision INTEGER,state TEXT,running_turn_id TEXT,deleted_at TEXT,history_purged_at TEXT,model TEXT,outcome TEXT);`);
  db.prepare('INSERT INTO agents VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run(row.agent_id,row.workspace_id,'actual-name','2026-10-09T01:00:00Z',row.repo,
    row.worktree_path,row.branch,row.harness,row.harness_session_id,'',null,null,'user',null,'lead',1,'idle',null,null,null,'actual/model',null);
  const native=createNativeHostAccess({configRoot:root,workspaceId:'workspace',repo:row.repo,pinnedExecutable:'/owned/opencode'});
  const actual=await native.agentIdentity!('agt_owned',new AbortController().signal);
  assert.equal(actual.name,'actual-name');assert.equal(actual.created_at,'2026-10-09T01:00:00Z');
  assert.equal(Object.hasOwn(await native.rawAgent('agt_owned',new AbortController().signal),'name'),false);assert.throws(()=>NativeAgentIdentity.parse(row));
  await assert.rejects(native.agentIdentity!('foreign',new AbortController().signal));
  db.exec("UPDATE agents SET repo='/foreign'");await assert.rejects(native.agentIdentity!('agt_owned',new AbortController().signal));
  db.exec("UPDATE agents SET repo='/owned/source'; INSERT INTO agents SELECT * FROM agents");await assert.rejects(native.agentIdentity!('agt_owned',new AbortController().signal),/duplicated/);
});
