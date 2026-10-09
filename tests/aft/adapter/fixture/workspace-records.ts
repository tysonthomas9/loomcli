import { z } from 'zod';
import { fixtureOwnerIdentity, type FixtureAuthorityOwner } from '../authority.js';
import { createOwnedWorkspaceRoster, LegacyWorkspaceAgentFact, WorkspaceCreationFact, type OwnedWorkspaceRoster } from '../workspaces.js';
import type { EvidenceStore } from '../evidence.js';
import { FixtureError } from './lifecycle.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const Repo=z.object({path:z.string().min(1)}).passthrough();
const Workspace=z.object({id:z.string().min(1),repos:z.array(Repo).min(1).max(32)}).passthrough();
const Envelope=z.object({success:z.literal(true),data:Workspace}).passthrough();
const Agent=z.object({workspace_key:z.string().min(1),name:z.string().min(1),parent:z.string().optional(),
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
 private readonly created=new Map<string,{workspaceId:string;repo:string;commonDir:string;store:WorkspaceStoreIdentity}>();
 constructor(private readonly ports:WorkspaceRecordPorts){}
 private async requireStore(expected:WorkspaceStoreIdentity,signal:AbortSignal){
  signal.throwIfAborted();const actual=await this.ports.store(signal);
  check(actual.storeId===expected.storeId&&actual.storeGeneration===expected.storeGeneration);return actual;
 }
 async captureCreated(workspaceId:string,sourceRepo:string,response:{status:number;body:unknown},before:WorkspaceStoreIdentity,signal:AbortSignal){
  check(!this.created.has(workspaceId)&&response.status===201);
  const created=Envelope.parse(response.body).data;check(created.id===workspaceId&&created.repos.length===1);
  const repo=created.repos[0]!.path;
  const commonDir=await this.ports.commonDir(repo,signal),sourceCommon=await this.ports.commonDir(sourceRepo,signal);
  check(commonDir===sourceCommon);
  const read=await this.ports.read(workspaceId,'workspace',signal);check(read.status===200);
  const current=Envelope.parse(read.body).data;
  check(current.id===workspaceId&&current.repos.length===1&&current.repos[0]!.path===repo);
  await this.requireStore(before,signal);
  this.created.set(workspaceId,Object.freeze({workspaceId,repo,commonDir,store:Object.freeze({...before})}));
  return repo;
 }
 async roster(owner:FixtureAuthorityOwner,store:EvidenceStore,signal:AbortSignal):Promise<OwnedWorkspaceRoster>{
  check(this.created.size>0);const records=[];
  for(const record of this.created.values()){
   await this.requireStore(record.store,signal);
   const fact=WorkspaceCreationFact.parse({kind:'workspace-created',...fixtureOwnerIdentity(owner),identityKind:'legacy-agent-name',
    workspaceId:record.workspaceId,repo:record.repo,commonDir:record.commonDir,...record.store,agentIds:[]});
   const receipt=await store.retain(JSON.stringify(fact));
   records.push({identityKind:fact.identityKind,workspaceId:fact.workspaceId,repo:fact.repo,commonDir:fact.commonDir,
    storeId:fact.storeId,storeGeneration:fact.storeGeneration,agentIds:[],creationReceipt:receipt});
  }
  return createOwnedWorkspaceRoster(owner,records,store);
 }
 async legacyAgent(owner:FixtureAuthorityOwner,workspaceId:string,name:string,signal:AbortSignal){
  const record=this.created.get(workspaceId);check(record);await this.requireStore(record!.store,signal);
  const read=await this.ports.read(workspaceId,'legacy-agents',signal);check(read.status===200);
  const list=z.object({success:z.literal(true),data:z.array(Agent).max(1000),total:z.number().int().nonnegative()}).passthrough().parse(read.body);
  check(list.total===list.data.length&&list.data.every(row=>row.workspace_key===workspaceId));
  const matches=list.data.filter(row=>row.name===name);check(matches.length===1);
  const row=matches[0]!;await this.requireStore(record!.store,signal);
  return LegacyWorkspaceAgentFact.parse({kind:'legacy-agent-enrolled',identityKind:'legacy-agent-name',...fixtureOwnerIdentity(owner),
   workspaceId,name:row.name,repo:record!.repo,commonDir:record!.commonDir,...record!.store,
   parentName:row.parent||null,createdAt:row.created_at,updatedAt:row.updated_at});
 }
}
