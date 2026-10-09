import assert from 'node:assert/strict';
import { test } from 'node:test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp,realpath,mkdir,rm,rename,symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { observeGitLifecycle } from './git-lifecycle.js';
import type { GitReader } from './git.js';
import { readContainerObservation,containerGitLifecycleObserver,ContainerObservationRequest } from './container-observations.js';
import { AgentRow } from './protocol.js';
const input={agent:{fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_owned'},maxBytes:10000};
async function fixture(t:{after(fn:()=>Promise<void>):void}) {
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-git-lifecycle-')));t.after(()=>rm(root,{recursive:true,force:true}));
  const sourceRoot=path.join(root,'source');const parent=path.join(root,'worktrees');await mkdir(sourceRoot);await mkdir(parent);
  const owned={sourceRoot,commonDir:path.join(sourceRoot,'.git'),branch:'owned',worktree:path.join(parent,'agt_owned')};
  return {root,parent,owned};
}
function reader(owned:{sourceRoot:string;commonDir:string;branch:string},hook?:(args:readonly string[])=>Promise<void>):GitReader {
  return {async read(args,cwd){assert.equal(cwd,owned.sourceRoot);await hook?.(args);
    if(args.includes('--git-common-dir'))return {code:0,stdout:owned.commonDir};
    if(args.includes('--show-toplevel'))return {code:0,stdout:owned.sourceRoot};
    if(args[0]==='check-ref-format'||args.includes('--quiet'))return {code:0,stdout:''};
    assert.deepEqual(args,['show-ref','--verify','refs/heads/owned']);return {code:0,stdout:`${'a'.repeat(40)} refs/heads/owned\n`};}};
}
test('surviving source reports actual retained branch and deleted checkout independently',async t=>{
  const {owned}=await fixture(t);const run=promisify(execFile);
  const env={...process.env,GIT_CONFIG_GLOBAL:'/dev/null',GIT_CONFIG_SYSTEM:'/dev/null',GIT_AUTHOR_NAME:'Fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',
    GIT_COMMITTER_NAME:'Fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid'};
  const git=async(args:string[])=> (await run('git',args,{cwd:owned.sourceRoot,env})).stdout.trim();
  await git(['init','--initial-branch=main']);
  const tree=await git(['hash-object','-w','-t','tree','/dev/null']);
  const commit=await git(['commit-tree',tree,'-m','Owned deterministic fixture']);
  await git(['update-ref','refs/heads/main',commit]);
  await git(['worktree','add','-b','owned',owned.worktree]);
  const before=await observeGitLifecycle(input,owned);assert.equal(before.worktreePresent,true);assert.equal(before.branchRef!.oid,commit);
  await git(['worktree','remove',owned.worktree]);
  const absent=await observeGitLifecycle(input,owned);assert.equal(absent.worktreePresent,false);assert.deepEqual(absent.branchRef,before.branchRef);
  await git(['update-ref','-d','refs/heads/owned']);
  const missing=await observeGitLifecycle(input,owned);assert.equal(missing.branchRef,null);assert.equal(missing.worktreePresent,false);
});
test('foreign repository, unreadable reads, symlink checkout and changed ref never prove absence',async t=>{
  const {owned}=await fixture(t);await mkdir(owned.commonDir);await mkdir(owned.worktree);
  await assert.rejects(observeGitLifecycle(input,owned,{read:async()=>({code:2,stdout:''})}));
  const valid=reader(owned);
  await assert.rejects(observeGitLifecycle(input,owned,{read:async(args,cwd,bound)=>args.includes('--show-toplevel')?{code:0,stdout:'/foreign'}:valid.read(args,cwd,bound)}));
  let refs=0;
  await assert.rejects(observeGitLifecycle(input,owned,{read:async(args,cwd,bound)=>args[0]==='show-ref'&&!args.includes('--quiet')?
    {code:0,stdout:`${(++refs===1?'a':'b').repeat(40)} refs/heads/owned`}:valid.read(args,cwd,bound)}));
  await rm(owned.worktree,{recursive:true});await symlink(owned.sourceRoot,owned.worktree);
  await assert.rejects(observeGitLifecycle(input,owned,valid));
});
test('replaced or symlink checkout parent is rejected despite same canonical path spelling',async t=>{
  const {owned,parent,root}=await fixture(t);await mkdir(owned.commonDir);
  let moved=false;const altered=reader(owned,async args=>{if(args[0]==='show-ref'&&!moved){moved=true;await rename(parent,path.join(root,'old-parent'));await mkdir(parent);}});
  await assert.rejects(observeGitLifecycle(input,owned,altered));
  await rm(parent,{recursive:true});await symlink(path.join(root,'old-parent'),parent);
  await assert.rejects(observeGitLifecycle(input,owned,reader(owned)));
});
test('container lifecycle request is closed and binds exact agent/store/source before fixed reads',async t=>{
  const {owned,parent}=await fixture(t);await mkdir(owned.commonDir);
  const row=AgentRow.parse({agent_id:'agt_owned',workspace_id:'workspace',repo:owned.sourceRoot,worktree_path:owned.worktree,branch:'owned',harness:'opencode',
    harness_session_id:'ses_owned',harness_session_root:'',parent_agent_id:null,root_agent_id:null,created_by_kind:'user',created_by_id:null,
    preset:'lead',revision:1,state:'idle',running_turn_id:null,deleted_at:null,history_purged_at:null});
  const unused=async():Promise<never>=>{throw new Error('unused native service');};
  const access={pinnedExecutable:'/owned/opencode',agent:async()=>row,registration:unused,process:unused,sessions:unused,read:unused};
  const paths={workspaceId:'workspace',repo:owned.sourceRoot,commonDir:owned.commonDir,worktreeParent:parent};let reads=0;
  const read=async(request:unknown)=>{reads++;return readContainerObservation(request,access,paths,reader(owned));};
  const observe=containerGitLifecycleObserver(read,input.agent,owned);
  assert.equal((await observe(input,new AbortController().signal)).worktreePresent,false);
  await assert.rejects(observe({...input,agent:{...input.agent,workspaceId:'foreign'}},new AbortController().signal));assert.equal(reads,1);
  assert.equal(ContainerObservationRequest.safeParse({operation:'git-lifecycle',agentId:'agt_owned',maxBytes:1000,query:'arbitrary'}).success,false);
  await assert.rejects(readContainerObservation({operation:'git-lifecycle',agentId:'agt_owned',maxBytes:1000},{...access,agent:async()=>({...row,repo:'/foreign'})},paths,reader(owned)));
});
