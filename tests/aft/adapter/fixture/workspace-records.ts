import { z } from 'zod';
import { isDeepStrictEqual } from 'node:util';
import path from 'node:path';
import { fixtureOwnerIdentity, type FixtureAuthorityOwner } from '../authority.js';
import { createOwnedWorkspaceRoster, LegacyWorkspaceAgentFact, WorkspaceCreationFact, WorkspaceRepositoryFact, WorkspaceRepositoriesAddedFact, readWorkspaceRepositoriesAddedFact, resolveLegacyRepositoryAssignments, type OwnedWorkspaceRoster, type OwnedWorkspaceRecord, type WorkspaceIdentityKind } from '../workspaces.js';
import type { EvidenceStore } from '../evidence.js';
import { FixtureError } from './lifecycle.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const Names=z.array(z.string().min(1)).max(32);
const Repo=z.object({name:z.string().min(1),path:z.string().min(1),source_repo_id:z.string().min(1),groups:Names}).passthrough();
const Workspace=z.object({id:z.string().min(1),repos:z.array(Repo).min(1).max(32)}).passthrough();
const Envelope=z.object({success:z.literal(true),data:Workspace}).passthrough();
const Agent=z.object({workspace_key:z.string().min(1),name:z.string().min(1),parent:z.string().optional(),
 repos:Names,repo_groups:Names,
 created_at:z.string().datetime({offset:true}),updated_at:z.string().datetime({offset:true})}).passthrough();
export interface WorkspaceStoreIdentity {storeId:string;storeGeneration:string;}
export interface WorkspaceRecordPorts {
 store(signal:AbortSignal):Promise<WorkspaceStoreIdentity>;
 read(workspaceId:string,view:'workspace'|'legacy-agents',signal:AbortSignal):Promise<{status:number;body:unknown}>;
 commonDir(repo:string,signal:AbortSignal):Promise<string>;
}
/** Private provisioning facts, not a public lease registry. Only successful
 * creation by the fixture can enter this collection; API discovery cannot. */
export class HostWorkspaceRecords {
 private readonly created=new Map<string,{workspaceId:string;repo:string;commonDir:string;store:WorkspaceStoreIdentity;
  repositories:NonNullable<OwnedWorkspaceRecord['repositories']>;creationReceipt?:OwnedWorkspaceRecord['creationReceipt']} >();
 constructor(private readonly ports:WorkspaceRecordPorts,private readonly identityKind:WorkspaceIdentityKind='legacy-agent-name'){}
 has(workspaceId:string){return this.created.has(workspaceId);}
 async workspace(workspaceId:string,signal:AbortSignal){
  const record=this.created.get(workspaceId);check(record);await this.requireStore(record!.store,signal);
  const response=await this.ports.read(workspaceId,'workspace',signal);check(response.status===200);
  const actual=Envelope.parse(response.body).data;
  check(actual.id===workspaceId&&isDeepStrictEqual(await this.repositories(actual,signal),record!.repositories));
  await this.requireStore(record!.store,signal);return record!;
 }
 private async requireStore(expected:WorkspaceStoreIdentity,signal:AbortSignal){
  signal.throwIfAborted();const actual=await this.ports.store(signal);
  check(actual.storeId===expected.storeId&&actual.storeGeneration===expected.storeGeneration);return actual;
 }
 private async repositories(actual:z.infer<typeof Workspace>,signal:AbortSignal){
  const names=new Set<string>(),paths=new Set<string>();const facts=[];
  for(const repo of actual.repos){
   check(!names.has(repo.name)&&!paths.has(repo.path)&&new Set(repo.groups).size===repo.groups.length);
   names.add(repo.name);paths.add(repo.path);
   facts.push(WorkspaceRepositoryFact.parse({repoName:repo.name,sourceRepoId:repo.source_repo_id,repo:repo.path,
    commonDir:await this.ports.commonDir(repo.path,signal),groups:[...repo.groups].sort()}));
  }
  return facts.sort((a,b)=>a.repoName<b.repoName?-1:a.repoName>b.repoName?1:0);
 }
 /** Read-only startup preflight over the finite code-owned source set. */
 async validateSourceRepositories(sources:readonly string[],signal:AbortSignal){
  signal.throwIfAborted();check(sources.length>0&&sources.length<=32&&new Set(sources).size===sources.length&&
   sources.every(source=>path.isAbsolute(source)&&path.normalize(source)===source));
  const commons:string[]=[];
  for(const source of sources)commons.push(await this.ports.commonDir(source,signal));
  check(new Set(commons).size===commons.length&&commons.every(common=>path.isAbsolute(common)&&path.normalize(common)===common));
  signal.throwIfAborted();return commons;
 }
 private async prepareCreated(workspaceId:string,sourceRepo:string|readonly string[],response:{status:number;body:unknown},before:WorkspaceStoreIdentity,signal:AbortSignal){
  check(!this.created.has(workspaceId)&&response.status===201);
  const sources=typeof sourceRepo==='string'?[sourceRepo]:[...sourceRepo];check(sources.length>0&&sources.length<=32&&new Set(sources).size===sources.length);
  const created=Envelope.parse(response.body).data;check(created.id===workspaceId&&created.repos.length===sources.length);
  const repositories=await this.repositories(created,signal),commons=await this.validateSourceRepositories(sources,signal);
  check(new Set(commons).size===commons.length&&repositories.every(repo=>commons.filter(common=>common===repo.commonDir).length===1));
  check(new Set(repositories.map(repo=>repo.commonDir)).size===repositories.length);
  // Retained workspace topology anchor only; actor affinity and physical
  // selection are resolved independently from actual assignments/diagnostics.
  const {repo,commonDir}=repositories[0]!;
  const read=await this.ports.read(workspaceId,'workspace',signal);check(read.status===200);
  const current=Envelope.parse(read.body).data;
  check(current.id===workspaceId&&isDeepStrictEqual(await this.repositories(current,signal),repositories));
  await this.requireStore(before,signal);
  return Object.freeze({workspaceId,repo,commonDir,store:Object.freeze({...before}),
   repositories:Object.freeze(repositories.map(value=>Object.freeze({...value,groups:Object.freeze([...value.groups])})))});
 }
 async captureCreated(workspaceId:string,sourceRepo:string|readonly string[],response:{status:number;body:unknown},before:WorkspaceStoreIdentity,signal:AbortSignal){
  const record=await this.prepareCreated(workspaceId,sourceRepo,response,before,signal);
  check(!this.created.has(workspaceId));this.created.set(workspaceId,record);return record.repo;
 }
 /** Only the native two-response setup calls this. The linked initial receipt
  * is authenticated before this collection gains physical authority. */
 async captureRepositoriesAdded(owner:FixtureAuthorityOwner,workspaceId:string,sources:readonly string[],response:{status:number;body:unknown},before:WorkspaceStoreIdentity,
  initialCreationReceipt:OwnedWorkspaceRecord['creationReceipt'],store:EvidenceStore,signal:AbortSignal,verifyOwnership:(signal:AbortSignal)=>Promise<void>){
  check(this.identityKind==='native-agent-id');
  const record=await this.prepareCreated(workspaceId,sources,response,before,signal);
  const fact=WorkspaceRepositoriesAddedFact.parse({kind:'workspace-repositories-added',...fixtureOwnerIdentity(owner),identityKind:this.identityKind,
   workspaceId,httpStatus:response.status,repo:record.repo,commonDir:record.commonDir,...record.store,
   repositories:record.repositories,agentIds:[],agentSources:[],initialCreationReceipt});
  const receipt=await store.retain(JSON.stringify(fact));
  await readWorkspaceRepositoriesAddedFact(owner,receipt,store);await this.requireStore(before,signal);
  await verifyOwnership(signal);check(!this.created.has(workspaceId));
  const retained=Object.freeze({...record,creationReceipt:receipt});this.created.set(workspaceId,retained);
  try{const result=await this.creationRecord(owner,workspaceId,store,signal);await verifyOwnership(signal);return result;}
  catch(error){if(this.created.get(workspaceId)===retained)this.created.delete(workspaceId);throw error;}
 }
 async creationRecord(owner:FixtureAuthorityOwner,workspaceId:string,store:EvidenceStore,signal:AbortSignal):Promise<OwnedWorkspaceRecord>{
   const record=await this.workspace(workspaceId,signal);
   const fact=WorkspaceCreationFact.parse({kind:'workspace-created',...fixtureOwnerIdentity(owner),identityKind:this.identityKind,
    workspaceId:record.workspaceId,repo:record.repo,commonDir:record.commonDir,...record.store,agentIds:[],repositories:record.repositories,agentSources:[]});
   const receipt=record.creationReceipt??await store.retain(JSON.stringify(fact));
   if(record.creationReceipt){const linked=await readWorkspaceRepositoriesAddedFact(owner,receipt,store);
    check(linked.workspaceId===record.workspaceId&&linked.repo===record.repo&&linked.commonDir===record.commonDir&&
     linked.storeId===record.store.storeId&&linked.storeGeneration===record.store.storeGeneration&&isDeepStrictEqual(linked.repositories,record.repositories));}
   return {identityKind:fact.identityKind,workspaceId:fact.workspaceId,repo:fact.repo,commonDir:fact.commonDir,
    storeId:fact.storeId,storeGeneration:fact.storeGeneration,agentIds:[],repositories:fact.repositories,agentSources:[],enrollmentReceipts:[],creationReceipt:receipt};
 }
 async roster(owner:FixtureAuthorityOwner,store:EvidenceStore,signal:AbortSignal):Promise<OwnedWorkspaceRoster>{
  check(this.created.size>0);const records=[];
  for(const record of this.created.values())records.push(await this.creationRecord(owner,record.workspaceId,store,signal));
  return createOwnedWorkspaceRoster(owner,records.map(record=>({...record,agentIds:[...record.agentIds],enrollmentReceipts:[...record.enrollmentReceipts]})),store);
 }
 async legacyAgent(owner:FixtureAuthorityOwner,workspaceId:string,name:string,signal:AbortSignal){
  check(this.identityKind==='legacy-agent-name');
  const record=await this.workspace(workspaceId,signal);
  const read=await this.ports.read(workspaceId,'legacy-agents',signal);check(read.status===200);
  const list=z.object({success:z.literal(true),data:z.array(Agent).max(1000),total:z.number().int().nonnegative()}).passthrough().parse(read.body);
  check(list.total===list.data.length&&list.data.every(row=>row.workspace_key===workspaceId));
  const matches=list.data.filter(row=>row.name===name);check(matches.length===1);
  const row=matches[0]!;
  const names=resolveLegacyRepositoryAssignments(record.repositories,row.repos,row.repo_groups);
  const source=names.length===1?record.repositories.find(repo=>repo.repoName===names[0]):null;
  check(names.length!==1||source);
  await this.workspace(workspaceId,signal);
  return LegacyWorkspaceAgentFact.parse({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...fixtureOwnerIdentity(owner),
   workspaceId,name:row.name,repo:source?.repo??null,commonDir:source?.commonDir??null,...record!.store,
   assignedRepos:row.repos,assignedRepoGroups:row.repo_groups,parentName:row.parent||null,createdAt:row.created_at,updatedAt:row.updated_at});
 }
 /** A fixed product diagnostic supplies the observed common directory. Actor
  * membership alone never selects a physical worktree or the topology anchor. */
 async legacyPhysicalSource(owner:FixtureAuthorityOwner,workspaceId:string,name:string,commonDir:string,signal:AbortSignal,repoName?:string){
  const actor=await this.legacyAgent(owner,workspaceId,name,signal),record=await this.workspace(workspaceId,signal);
  const names=resolveLegacyRepositoryAssignments(record.repositories,actor.assignedRepos,actor.assignedRepoGroups);
  const matches=record.repositories.filter(repo=>repo.commonDir===commonDir&&names.includes(repo.repoName));
  check(matches.length===1);const selected=matches[0]!;
  check((repoName===undefined||repoName===selected.repoName)&&
   (actor.repo===null||(actor.repo===selected.repo&&actor.commonDir===selected.commonDir)));
  return selected;
 }
}
