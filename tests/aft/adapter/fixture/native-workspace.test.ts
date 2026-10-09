import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { NativeEmptyWorkspaceSetup, nativeWorkspaceHttp } from './native-workspace.js';
import { NativeWorkspaceRecords } from './native-records.js';
import { captureNativeStore } from './native-store.js';
import { createEvidenceStore } from '../evidence.js';
import { readWorkspaceRepositoriesAddedFact, createOwnedWorkspaceRoster } from '../workspaces.js';
import type { HostFixtureDriver } from './host.js';

const signal=()=>new AbortController().signal;
const owner={leaseId:'native-fixture',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'agents-emulator'};
function deferred(){let resolve!:()=>void;const promise=new Promise<void>(value=>{resolve=value;});return {promise,resolve};}
async function setup(){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-setup-'));
 const workspaceParent=path.join(root,'workspaces');await fs.mkdir(workspaceParent);
 const parentStat=await fs.lstat(workspaceParent),parentRoot={path:workspaceParent,device:parentStat.dev,inode:parentStat.ino};
 const workspacePath=path.join(workspaceParent,'e2e-agv1-ghread');
 const directories:fs.FileHandle[]=[];let failClose=false;
 const files={...fs,async open(...args:Parameters<typeof fs.open>){const handle=await fs.open(...args),close=handle.close.bind(handle);
  handle.close=async()=>{if(failClose){failClose=false;throw Error('injected directory close failure');}await close();};
  directories.push(handle);return handle;}};
 const config=path.join(root,'configuration');await fs.mkdir(config);await fs.writeFile(path.join(config,'agents.db'),'private physical descriptor fixture');
 const stat=await fs.lstat(config),captures:(()=>Promise<void>)[]=[],cleanups:(()=>Promise<void>)[]=[];
 const captured=await captureNativeStore({path:config,device:stat.dev,inode:stat.ino},cleanup=>captures.push(cleanup),signal());
 await fs.mkdir(path.join(root,'evidence'));const evidence=await createEvidenceStore(path.join(root,'evidence'));
 const sources=[path.join(root,'source-alpha'),path.join(root,'source-beta')];
 for(const source of sources){await fs.mkdir(source);await fs.mkdir(path.join(source,'.git'));}
 const repos=[{name:'alpha',path:path.join(root,'managed-alpha'),source_repo_id:'actual-source-alpha',groups:['shared']},
  {name:'beta',path:path.join(root,'managed-beta'),source_repo_id:'actual-source-beta',groups:['shared']}];
 let currentRepos:typeof repos=[],generation='actual-owned-serve-generation',deleteStatus=200;
 let createHook:(()=>Promise<void>)|undefined,addHook:(()=>Promise<void>)|undefined,workspaceHook:(()=>void)|undefined;
 let agents:unknown[]=[],legacyAgents:unknown[]=[],next='',createId='E2E-AGV1-GHREAD',addStatus=201;
 const calls:{method:string;relative:string;body:unknown}[]=[];
 const driver:Pick<HostFixtureDriver,'requestOwnedHttp'>={async requestOwnedHttp(target,method,relative,body,abort,expected){
  abort.throwIfAborted();assert.equal(target,'api');assert.equal(expected,'actual-owned-serve-generation');
  assert.equal(generation,expected);calls.push({method,relative,body});
  if(method==='POST'&&relative==='/api/workspaces'){
   assert.deepEqual(body,{name:'e2e-agv1-ghread',type:'empty',repos:[]});await createHook?.();
   await fs.mkdir(workspacePath);return {status:201,body:{success:true,data:{id:createId,path:workspacePath,agents:legacyAgents,repos:[]}}};
  }
  if(method==='POST'){
   assert.equal(relative,'/api/workspaces/E2E-AGV1-GHREAD/repos');assert.deepEqual(body,{repos:sources});
   await addHook?.();currentRepos=structuredClone(repos);return {status:addStatus,body:{success:true,data:{id:createId,path:workspacePath,agents:legacyAgents,repos:structuredClone(currentRepos)}}};
  }
  if(method==='DELETE')return {status:deleteStatus,body:{success:deleteStatus===200}};
  if(relative.includes('/v1/agents'))return {status:200,body:{agents,next}};
  assert.equal(relative,'/api/workspaces/E2E-AGV1-GHREAD');workspaceHook?.();
  return {status:200,body:{success:true,data:{id:'E2E-AGV1-GHREAD',path:workspacePath,agents:legacyAgents,repos:structuredClone(currentRepos)}}};
 }};
 const api=nativeWorkspaceHttp(driver,generation);
 const records=new NativeWorkspaceRecords({async store(abort){await captured.verify(abort);return captured;},
  async read(ws,view,abort){assert.equal(view,'workspace');return api.readWorkspace(ws,abort);},
  async commonDir(repo){let index=sources.indexOf(repo);if(index<0)index=repos.findIndex(value=>value.path===repo);
   assert.ok(index>=0);return path.join(sources[index]!,'.git');}
 },async()=>{throw Error('No native row or process observation used by empty setup');});
 const provisioning=new NativeEmptyWorkspaceSetup(owner,[{name:'e2e-agv1-ghread',workspaceId:'E2E-AGV1-GHREAD',sources}],
  records,captured,evidence,api,parentRoot,cleanup=>cleanups.push(cleanup),files);
 return {root,captured,evidence,records,provisioning,calls,repos,sources,cleanups,parentRoot,workspacePath,
  createHook:(hook:()=>Promise<void>)=>{createHook=hook;},addHook:(hook:()=>Promise<void>)=>{addHook=hook;},
  workspaceHook:(hook:()=>void)=>{workspaceHook=hook;},setGeneration:(value:string)=>{generation=value;},
  setAgents:(rows:unknown[],cursor='')=>{agents=rows;next=cursor;},setCreateId:(value:string)=>{createId=value;},
  setLegacyAgents:(rows:unknown[])=>{legacyAgents=rows;},
  setAddStatus:(value:number)=>{addStatus=value;},setDeleteStatus:(value:number)=>{deleteStatus=value;},
  failDirectoryClose:()=>{failClose=true;},
  mutateTopology:()=>{currentRepos[1]!.source_repo_id='foreign-current-source';},
  provision:()=>provisioning.provision('E2E-AGV1-GHREAD',signal()),
  async remove(){for(const cleanup of captures)await cleanup();for(const handle of directories)await handle.close();await fs.rm(root,{recursive:true,force:true});}};
}

test('production two-stage setup retains distinct actual 201 responses and canonical named physical roster',async()=>{
 const s=await setup();try{
  const record=await s.provision();const final=await readWorkspaceRepositoriesAddedFact(owner,record.creationReceipt,s.evidence);
  assert.equal(final.kind,'workspace-repositories-added');assert.equal(final.httpStatus,201);
  assert.notEqual(final.initialCreationReceipt.sha256,record.creationReceipt.sha256);
  const initial=JSON.parse(await fs.readFile(await s.evidence.resolveBounded(final.initialCreationReceipt,4_000_000),'utf8'));
  assert.equal(initial.kind,'workspace-created-empty');assert.deepEqual(initial.repositories,[]);
  assert.deepEqual(final.repositories.map(repo=>[repo.repoName,repo.sourceRepoId]),[['alpha','actual-source-alpha'],['beta','actual-source-beta']]);
  const roster=await s.records.roster(owner,s.evidence,signal());assert.equal(roster[0]!.creationReceipt.sha256,record.creationReceipt.sha256);
  await assert.rejects(createOwnedWorkspaceRoster(owner,[{...record,creationReceipt:final.initialCreationReceipt}],s.evidence));
  assert.deepEqual(s.calls.filter(call=>call.method==='POST').map(call=>call.relative),['/api/workspaces','/api/workspaces/E2E-AGV1-GHREAD/repos']);
  assert.equal(s.cleanups.length,1);await s.cleanups[0]!();await s.cleanups[0]!();
  assert.equal(s.calls.filter(call=>call.method==='DELETE').length,1);
 }finally{await s.remove();}
});

test('concurrent and repeated acquisition cannot replay either repository or creation mutation',async()=>{
 const s=await setup();const started=deferred(),release=deferred();try{
  s.createHook(async()=>{started.resolve();await release.promise;});const first=s.provision();await started.promise;
  await assert.rejects(s.provision());release.resolve();await first;await assert.rejects(s.provision());
  assert.equal(s.calls.filter(call=>call.method==='POST').length,2);
 }finally{release.resolve();await s.remove();}
});

test('foreign creation, incomplete agent list and changed store deny physical authority before repository write',async()=>{
 for(const change of ['foreign','agent','cursor','store'] as const){
  const s=await setup();try{
   if(change==='foreign')s.setCreateId('FOREIGN');if(change==='agent')s.setAgents([{agent_id:'actual-unowned-agent'}]);
   if(change==='cursor')s.setAgents([],'more');if(change==='store')await s.captured.close();
   await assert.rejects(s.provision());assert.equal(s.records.has('E2E-AGV1-GHREAD'),false);
   assert.equal(s.calls.filter(call=>call.method==='POST'&&call.relative.endsWith('/repos')).length,0);
   if(change!=='store'){await assert.rejects(s.cleanups[0]!());assert.equal(s.calls.filter(call=>call.method==='DELETE').length,0);}
  }finally{await s.remove();}
 }
});

test('uncertain repository response is retained and never replayed or used as cleanup topology',async()=>{
 for(const change of ['throw','status','topology'] as const){
  const s=await setup();try{
   if(change==='throw')s.addHook(async()=>{throw Error('uncertain response');});
   if(change==='status')s.setAddStatus(500);
   if(change==='topology')s.addHook(async()=>{s.workspaceHook(s.mutateTopology);});
   await assert.rejects(s.provision());await assert.rejects(s.provision());await assert.rejects(s.cleanups[0]!());
   assert.equal(s.records.has('E2E-AGV1-GHREAD'),false);
   assert.equal(s.calls.filter(call=>call.method==='POST'&&call.relative.endsWith('/repos')).length,1);
   assert.equal(s.calls.filter(call=>call.method==='DELETE').length,0);
   const diagnostic=s.provisioning.diagnostics()[0]!;assert.ok(diagnostic.initialCreationReceipt);
   assert.equal(diagnostic.repositoryWriteAttempted,true);assert.equal(diagnostic.complete,false);
  }finally{await s.remove();}
 }
});

test('disposal waits pending creation, prevents repository dispatch and removes only captured empty ownership',async()=>{
 const s=await setup();const started=deferred(),release=deferred();try{
  s.createHook(async()=>{started.resolve();await release.promise;});
  const first=assert.rejects(s.provision());await started.promise;const closed=s.cleanups[0]!();release.resolve();await first;await closed;
  assert.equal(s.calls.filter(call=>call.method==='POST').length,1);assert.equal(s.calls.filter(call=>call.method==='DELETE').length,1);
  assert.equal(s.provisioning.diagnostics()[0]!.deleted,true);
 }finally{release.resolve();await s.remove();}
});

test('cleanup failure retains exact ownership for retry and refuses a replacement serve generation',async()=>{
 const s=await setup();try{
  await s.provision();s.setDeleteStatus(500);await assert.rejects(s.cleanups[0]!());
  assert.equal(s.provisioning.diagnostics()[0]!.deleted,false);
  s.setGeneration('replacement');await assert.rejects(s.cleanups[0]!());assert.equal(s.calls.filter(call=>call.method==='DELETE').length,1);
  s.setGeneration('actual-owned-serve-generation');s.setDeleteStatus(200);await s.cleanups[0]!();
  assert.equal(s.calls.filter(call=>call.method==='DELETE').length,2);assert.equal(s.provisioning.diagnostics()[0]!.deleted,true);
 }finally{await s.remove();}
});

test('same-key directory replacement and foreign actors reject cleanup without deleting foreign resources',async()=>{
 for(const change of ['directory','actor','legacy-actor']){
  const s=await setup();try{
   await s.provision();
   if(change==='directory'){await fs.rename(s.workspacePath,s.workspacePath+'.retained');await fs.mkdir(s.workspacePath);}
   else if(change==='actor')s.setAgents([{agent_id:'actual-later-actor'}]);
   else s.setLegacyAgents([{name:'actual-later-legacy-actor'}]);
   await assert.rejects(s.cleanups[0]!());assert.equal(s.calls.filter(call=>call.method==='DELETE').length,0);
   assert.equal(s.provisioning.diagnostics()[0]!.deleted,false);
  }finally{await s.remove();}
 }
});

test('failed directory disposal retains the exact handle for retry without replaying workspace deletion',async()=>{
 const s=await setup();try{
  await s.provision();s.failDirectoryClose();await assert.rejects(s.cleanups[0]!());
  assert.equal(s.provisioning.diagnostics()[0]!.deleted,true);assert.equal(s.provisioning.diagnostics()[0]!.complete,false);
  await s.cleanups[0]!();assert.equal(s.provisioning.diagnostics()[0]!.complete,true);
  assert.equal(s.calls.filter(call=>call.method==='DELETE').length,1);
 }finally{await s.remove();}
});

test('source association failure is a pre-mutation failure with no resource cleanup command',async()=>{
 const s=await setup();try{
  s.records.validateSourceRepositories=async()=>{throw Error('foreign source identity');};
  await assert.rejects(s.provision());assert.equal(s.calls.length,0);await s.cleanups[0]!();
  assert.equal(s.calls.length,0);assert.equal(s.provisioning.diagnostics()[0]!.complete,true);
 }finally{await s.remove();}
});

test('directory replacement during final receipt I/O leaves no authoritative physical record',async()=>{
 const s=await setup();try{
  const retain=s.evidence.retain.bind(s.evidence);
  s.evidence.retain=async serialized=>{
   const result=await retain(serialized);
   if(JSON.parse(serialized).kind==='workspace-repositories-added'){
    await fs.rename(s.workspacePath,s.workspacePath+'.retained');await fs.mkdir(s.workspacePath);
   }
   return result;
  };
  await assert.rejects(s.provision());assert.equal(s.records.has('E2E-AGV1-GHREAD'),false);
  assert.equal(s.provisioning.diagnostics()[0]!.complete,false);
  await assert.rejects(s.cleanups[0]!());assert.equal(s.calls.filter(call=>call.method==='DELETE').length,0);
 }finally{await s.remove();}
});

test('foreign, empty or malformed trusted plans reject before any mutation',async()=>{
 const s=await setup();try{
  await assert.rejects(s.provisioning.provision('FOREIGN',signal()));assert.equal(s.calls.length,0);
  const api=nativeWorkspaceHttp({requestOwnedHttp:async()=>{throw Error('No effect permitted');}},'actual-generation');
  for(const plan of [{name:'e2e-agv1-ghread',workspaceId:'FOREIGN',sources:s.sources},
   {name:'e2e-agv1-ghread',workspaceId:'E2E-AGV1-GHREAD',sources:[]},
   {name:'e2e-agv1-ghread',workspaceId:'E2E-AGV1-GHREAD',sources:['relative']}]){
   assert.throws(()=>new NativeEmptyWorkspaceSetup(owner,[plan],s.records,s.captured,s.evidence,api,s.parentRoot,()=>{}));
  }
 }finally{await s.remove();}
});
