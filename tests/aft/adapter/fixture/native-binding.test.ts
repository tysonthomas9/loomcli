import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { captureNativeWorkspaceBinding, captureNativeEmptyWorkspaceSetup } from './native-binding.js';
import { createEvidenceStore } from '../evidence.js';
import { enrollOwnedWorkspaceAgent, requireOwnedWorkspace } from '../workspaces.js';
import type { OwnedFixture } from '../ownership.js';
import type { HostFixtureDriver } from './host.js';

const signal=()=>new AbortController().signal;
const owner={leaseId:'native-owned',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'agents-emulator'};
async function setup(){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-binding-'));
 const config=path.join(root,'configuration');await fs.mkdir(config);await fs.mkdir(path.join(root,'evidence'));
 const configStat=await fs.lstat(config),cleanups:(()=>Promise<void>)[]=[];
 const sources=[path.join(root,'source-alpha'),path.join(root,'source-beta')];
 const repositories=[{name:'alpha',path:path.join(root,'managed-alpha'),source_repo_id:'source-alpha-id',groups:['shared']},
  {name:'beta',path:path.join(root,'managed-beta'),source_repo_id:'source-beta-id',groups:['shared']}];
 for(const directory of [...sources,...repositories.map(repo=>repo.path)])await fs.mkdir(directory);
 for(const source of sources)await fs.mkdir(path.join(source,'.git'));
 const filename=path.join(config,'agents.db'),db=new DatabaseSync(filename);
 db.exec(`CREATE TABLE agents (agent_id TEXT, workspace_id TEXT, repo TEXT, worktree_path TEXT, branch TEXT, harness TEXT,
  harness_session_id TEXT, harness_session_root TEXT, parent_agent_id TEXT, root_agent_id TEXT, created_by_kind TEXT, created_by_id TEXT,
  preset TEXT, revision INTEGER, state TEXT, running_turn_id TEXT, deleted_at TEXT, history_purged_at TEXT, model TEXT, outcome TEXT);`);
 db.prepare('INSERT INTO agents VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run('agt_beta','NATIVE-WS',repositories[1]!.path,
  path.join(root,'beta-worktree'),'loom/agent/beta','opencode','ses_beta','',null,null,'user','actual-user','lead',4,'idle',null,null,null,'observed/model',null);
 db.close();
 const body={success:true,data:{id:'NATIVE-WS',repos:repositories}};
 let closes=0,reads=0;
 const files={...fs,async open(...args:Parameters<typeof fs.open>){
  const handle=await fs.open(...args),close=handle.close.bind(handle);
  handle.close=async()=>{closes++;await close();};return handle;
 }};
 const binding=await captureNativeWorkspaceBinding({owner,
  configurationRoot:{path:config,device:configStat.dev,inode:configStat.ino},pinnedExecutable:'/pinned/opencode',
  enrollCleanup:cleanup=>cleanups.push(cleanup),processIdentity:async()=>{throw Error('No OS process lookup permitted');},
  fetch:async()=>{throw Error('No HTTP/native service request permitted');}},
  {async read(workspaceId,view){reads++;assert.equal(view,'workspace');assert.equal(workspaceId,'NATIVE-WS');return {status:200,body};},
   async commonDir(repo){const index= repositories.findIndex(value=>value.path===repo),sourceIndex=sources.indexOf(repo);
    assert.ok(index>=0||sourceIndex>=0);return path.join(sources[index>=0?index:sourceIndex]!,'.git');}},signal(),files);
 const capture=()=>binding.records.captureCreated('NATIVE-WS',sources,{status:201,body},
  {storeId:binding.store.storeId,storeGeneration:binding.store.storeGeneration},signal());
 return {root,config,filename,body,repositories,binding,cleanups,capture,closes:()=>closes,reads:()=>reads,
  async remove(){for(const cleanup of cleanups)await cleanup();await fs.rm(root,{recursive:true,force:true});}};
}

test('production captured-store and rawAgent composition supplies canonical native enrollment from SQLite',async()=>{
 const r=await setup();try{
  await r.capture();const evidence=await createEvidenceStore(path.join(r.root,'evidence'));
  const roster=await r.binding.records.roster(owner,evidence,signal());
  const fixture:OwnedFixture={...owner,workspaceId:'NATIVE-WS',repo:roster[0]!.repo,ownedWorkspaces:roster,
   expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',roots:new Map(),agents:new Map(),secrets:[],
   readApi:async()=>{throw Error('unused');},readFiles:async()=>{throw Error('unused');},
   verify:async()=>{},dispose:async()=>{},resolveAgent:async()=>{throw Error('unused');},
   readWorkspaceAgent:r.binding.readWorkspaceAgent};
  await enrollOwnedWorkspaceAgent(fixture,'NATIVE-WS','agt_beta',signal(),evidence);
  assert.equal(requireOwnedWorkspace(fixture,'NATIVE-WS','agt_beta').repo,r.repositories[1]!.path);
  const fact=await r.binding.readWorkspaceAgent('NATIVE-WS','agt_beta',signal());
  assert.equal(fact.createdById,'actual-user');assert.equal(fact.revision,4);
  const access=await r.binding.nativeAccess('NATIVE-WS',signal());
  assert.equal((await access.rawAgent('agt_beta',signal())).model,'observed/model');
  const update=new DatabaseSync(r.filename);update.exec("UPDATE agents SET revision=5,model='updated/model' WHERE agent_id='agt_beta'");update.close();
  assert.equal((await r.binding.readWorkspaceAgent('NATIVE-WS','agt_beta',signal())).revision,5);
  assert.equal(r.cleanups.length,1);await r.cleanups[0]!();await r.cleanups[0]!();assert.equal(r.closes(),1);
  await assert.rejects(r.binding.readWorkspaceAgent('NATIVE-WS','agt_beta',signal()));
 }finally{await r.remove();}
});

test('unknown workspace and foreign creation tuple cannot grant access to the captured store',async()=>{
 const r=await setup();try{
  await assert.rejects(r.binding.nativeAccess('foreign',signal()));assert.equal(r.reads(),0);
  await assert.rejects(r.binding.records.captureCreated('NATIVE-WS',[path.join(r.root,'source-alpha'),path.join(r.root,'source-beta')],
   {status:201,body:r.body},{storeId:r.binding.store.storeId,storeGeneration:'foreign'},signal()));
  await assert.rejects(r.binding.nativeAccess('NATIVE-WS',signal()));
 }finally{await r.remove();}
});

test('replacement database, foreign row and changed topology reject instead of yielding missing facts',async()=>{
 for(const change of ['database','row','topology'] as const){
  const r=await setup();try{
   await r.capture();
   if(change==='database'){await fs.rename(r.filename,r.filename+'.retained');const db=new DatabaseSync(r.filename);db.close();}
   if(change==='row'){const db=new DatabaseSync(r.filename);db.exec("UPDATE agents SET repo='/foreign/source' WHERE agent_id='agt_beta'");db.close();}
   if(change==='topology')r.repositories[1]!.source_repo_id='foreign-source';
   await assert.rejects(r.binding.readWorkspaceAgent('NATIVE-WS','agt_beta',signal()));
   if(change==='database')assert.equal(r.closes(),0);
  }finally{await r.remove();}
 }
});

test('aborted queries and disposed captures cannot reuse a still-present database',async()=>{
 const r=await setup();try{
  await r.capture();await assert.rejects(r.binding.readWorkspaceAgent('NATIVE-WS','agt_beta',AbortSignal.abort()));
  const access=await r.binding.nativeAccess('NATIVE-WS',signal());await r.cleanups[0]!();
  await assert.rejects(access.rawAgent('agt_beta',signal()));assert.equal(r.closes(),1);
 }finally{await r.remove();}
});

test('production empty-setup factory binds separate fixed HTTP writes to descriptor-backed SQLite native enrollment',async()=>{
 const r=await setup();const cleanups:(()=>Promise<void>)[]=[];try{
  const database=new DatabaseSync(r.filename);database.exec('DELETE FROM agents');database.close();
  const workspaceParent=path.join(r.root,'native-workspaces');await fs.mkdir(workspaceParent);
  const parentStat=await fs.lstat(workspaceParent),workspacePath=path.join(workspaceParent,'native-ws');
  const sources=[path.join(r.root,'source-alpha'),path.join(r.root,'source-beta')];
  const configStat=await fs.lstat(r.config),evidence=await createEvidenceStore(path.join(r.root,'evidence'));
  let added=false;const writes:string[]=[];
  const driver:Pick<HostFixtureDriver,'requestOwnedHttp'>={async requestOwnedHttp(target,method,relative,body,abort,generation){
   abort.throwIfAborted();assert.equal(target,'api');assert.equal(generation,'actual-source-serve-generation');
   if(method==='POST'){
    writes.push(relative);
    if(relative==='/api/workspaces'){assert.deepEqual(body,{name:'native-ws',type:'empty',repos:[]});await fs.mkdir(workspacePath);}
    else{assert.equal(relative,'/api/workspaces/NATIVE-WS/repos');assert.deepEqual(body,{repos:sources});added=true;}
   }
   if(relative.includes('/v1/agents')){
    const db=new DatabaseSync(r.filename,{readOnly:true});
    try{return {status:200,body:{agents:db.prepare('SELECT agent_id FROM agents WHERE workspace_id=?').all('NATIVE-WS'),next:''}};}
    finally{db.close();}
   }
   if(method==='DELETE')return {status:200,body:{success:true}};
   return {status:method==='POST'?201:200,body:{success:true,data:{id:'NATIVE-WS',path:workspacePath,agents:[],repos:added?r.repositories:[]}}};
  }};
  const binding=await captureNativeEmptyWorkspaceSetup({owner,configurationRoot:{path:r.config,device:configStat.dev,inode:configStat.ino},
   pinnedExecutable:'/pinned/opencode',enrollCleanup:cleanup=>cleanups.push(cleanup),
   processIdentity:async()=>{throw Error('No OS process lookup');},fetch:async()=>{throw Error('No HTTP/backend launch');}},
   [{name:'native-ws',workspaceId:'NATIVE-WS',sources}],evidence,driver,'actual-source-serve-generation',async repo=>{
    let index=sources.indexOf(repo);if(index<0)index=r.repositories.findIndex(value=>value.path===repo);
    assert.ok(index>=0);return path.join(sources[index]!,'.git');
   },{path:workspaceParent,device:parentStat.dev,inode:parentStat.ino},signal());
  const record=await binding.provisionWorkspace('NATIVE-WS',signal());
  assert.deepEqual(writes,['/api/workspaces','/api/workspaces/NATIVE-WS/repos']);
  const update=new DatabaseSync(r.filename);
  update.prepare('INSERT INTO agents VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run('agt_beta','NATIVE-WS',r.repositories[1]!.path,
   path.join(r.root,'beta-worktree'),'loom/agent/beta','opencode','ses_beta','',null,null,'user','actual-user','lead',4,'idle',null,null,null,'observed/model',null);
  update.close();
  const roster=await binding.records.roster(owner,evidence,signal());assert.equal(roster[0]!.creationReceipt.sha256,record.creationReceipt.sha256);
  const fixture:OwnedFixture={...owner,workspaceId:'NATIVE-WS',repo:record.repo,ownedWorkspaces:roster,
   expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',roots:new Map(),agents:new Map(),secrets:[],
   verify:async()=>{},dispose:async()=>{},readApi:async()=>{throw Error('unused');},readFiles:async()=>{throw Error('unused');},
   resolveAgent:async()=>{throw Error('unused');},readWorkspaceAgent:binding.readWorkspaceAgent};
  await enrollOwnedWorkspaceAgent(fixture,'NATIVE-WS','agt_beta',signal(),evidence);
  assert.equal(requireOwnedWorkspace(fixture,'NATIVE-WS','agt_beta').repo,r.repositories[1]!.path);
  assert.equal((await binding.readWorkspaceAgent('NATIVE-WS','agt_beta',signal())).createdById,'actual-user');
 }finally{
  // Remove only this test's temporary SQLite row before private-directory
  // disposal. This is not a product archive/cancel or backend-exit proof.
  const db=new DatabaseSync(r.filename);db.exec('DELETE FROM agents');db.close();
  for(const cleanup of cleanups.reverse())await cleanup();await r.remove();
 }
});
