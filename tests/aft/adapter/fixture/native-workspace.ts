import path from 'node:path';
import * as fs from 'node:fs/promises';
import { constants } from 'node:fs';
import { z } from 'zod';
import { fixtureOwnerIdentity, type FixtureAuthorityOwner } from '../authority.js';
import type { EvidenceStore } from '../evidence.js';
import { WorkspaceEmptyCreationFact, type OwnedWorkspaceRecord } from '../workspaces.js';
import { FixtureError } from './lifecycle.js';
import type { NativeWorkspaceRecords } from './native-records.js';
import type { NativeStoreBinding } from './native-store.js';
import type { HostFixtureDriver } from './host.js';
import type { OwnedRoot } from '../ownership.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const WorkspacePath=z.object({success:z.literal(true),data:z.object({id:z.string().min(1),path:z.string().min(1),agents:z.array(z.unknown()).length(0)}).passthrough()}).passthrough();
const Empty=z.object({success:z.literal(true),data:WorkspacePath.shape.data.extend({repos:z.array(z.unknown()).length(0)})}).passthrough();
const NoAgents=z.object({agents:z.array(z.unknown()).length(0),next:z.literal('')}).passthrough();
export interface NativeWorkspacePlan {readonly name:string;readonly workspaceId:string;readonly sources:readonly string[];}
type Request=HostFixtureDriver['requestOwnedHttp'];
/** Code-owned bridge to the existing generation-checked API transport. Neither
 * an origin, executable nor arbitrary request path is supplied by suite data. */
export function nativeWorkspaceHttp(driver:Pick<HostFixtureDriver,'requestOwnedHttp'>,serveGeneration:string){
 check(typeof serveGeneration==='string'&&serveGeneration.length>0);
 const request:Request=driver.requestOwnedHttp.bind(driver);
 const ws=(id:string)=>{check(/^[A-Z][A-Z0-9-]{0,31}$/.test(id));return `/api/workspaces/${encodeURIComponent(id)}`;};
 return Object.freeze({
  createEmpty:(name:string,signal:AbortSignal)=>request('api','POST','/api/workspaces',{name,type:'empty',repos:[]},signal,serveGeneration),
  addRepositories:(id:string,sources:readonly string[],signal:AbortSignal)=>request('api','POST',`${ws(id)}/repos`,{repos:[...sources]},signal,serveGeneration),
  readWorkspace:(id:string,signal:AbortSignal)=>request('api','GET',ws(id),null,signal,serveGeneration),
  readAgents:(id:string,signal:AbortSignal)=>request('api','GET',`${ws(id)}/v1/agents?include_archived=true&limit=1`,null,signal,serveGeneration),
  deleteWorkspace:(id:string,signal:AbortSignal)=>request('api','DELETE',ws(id),null,signal,serveGeneration),
 });
}
type Api=ReturnType<typeof nativeWorkspaceHttp>;
type Pending={initial?:OwnedWorkspaceRecord['creationReceipt'];created:boolean;added:boolean;deleted:boolean;completed:boolean;
 directory?:{root:OwnedRoot;handle:fs.FileHandle};};
/** Private startup transaction, not a public lease or workspace registry.
 * The finite plan is supplied by trusted provisioning after source creation.
 * Once a write might start, this instance never retries that acquisition. */
export class NativeEmptyWorkspaceSetup {
 private readonly plans:readonly NativeWorkspacePlan[];
 private readonly owner:Readonly<FixtureAuthorityOwner>;
 private readonly attempted=new Map<string,Pending>();
 private active=false;
 private finished?:Promise<void>;
 private disposed=false;
 private closing?:Promise<void>;
 private readonly workspaceParent:Readonly<OwnedRoot>;
 constructor(owner:FixtureAuthorityOwner,plans:readonly NativeWorkspacePlan[],
  private readonly records:NativeWorkspaceRecords,private readonly captured:NativeStoreBinding,
  private readonly evidence:EvidenceStore,private readonly api:Api,workspaceParent:OwnedRoot,
  enrollCleanup:(cleanup:()=>Promise<void>)=>void,private readonly files:typeof fs=fs){
  this.owner=Object.freeze(fixtureOwnerIdentity(owner));
  check(path.isAbsolute(workspaceParent.path)&&path.normalize(workspaceParent.path)===workspaceParent.path);
  this.workspaceParent=Object.freeze({...workspaceParent});
  check(plans.length>0&&plans.length<=32&&new Set(plans.map(plan=>plan.workspaceId)).size===plans.length);
  this.plans=Object.freeze(plans.map(plan=>{
   check(/^[a-z][a-z0-9-]{0,31}$/.test(plan.name)&&plan.workspaceId===plan.name.toUpperCase());
   check(plan.sources.length>0&&plan.sources.length<=32&&new Set(plan.sources).size===plan.sources.length&&
    plan.sources.every(source=>path.isAbsolute(source)&&path.normalize(source)===source));
   return Object.freeze({...plan,sources:Object.freeze([...plan.sources])});
  }));
  enrollCleanup(()=>this.close());
 }
 private async parent(signal:AbortSignal){
  signal.throwIfAborted();const stat=await this.files.lstat(this.workspaceParent.path);
  check(stat.isDirectory()&&!stat.isSymbolicLink()&&stat.dev===this.workspaceParent.device&&stat.ino===this.workspaceParent.inode&&
   await this.files.realpath(this.workspaceParent.path)===this.workspaceParent.path);
 }
 private async directory(pending:Pending,signal:AbortSignal){
  await this.parent(signal);check(pending.directory);
  const {root,handle}=pending.directory!,retained=await handle.stat(),named=await this.files.lstat(root.path);
  check(named.isDirectory()&&!named.isSymbolicLink()&&retained.isDirectory()&&named.dev===root.device&&named.ino===root.inode&&
   retained.dev===root.device&&retained.ino===root.inode&&await this.files.realpath(root.path)===root.path);
  await this.parent(signal);signal.throwIfAborted();
 }
 private async noAgents(workspaceId:string,signal:AbortSignal){
  const response=await this.api.readAgents(workspaceId,signal);check(response.status===200);NoAgents.parse(response.body);
 }
 private async empty(workspaceId:string,signal:AbortSignal){
  await this.captured.verify(signal);const pending=this.attempted.get(workspaceId)!;await this.directory(pending,signal);
  const response=await this.api.readWorkspace(workspaceId,signal);check(response.status===200);
  const actual=Empty.parse(response.body).data;
  check(actual.id===workspaceId&&actual.path===pending.directory!.root.path);
  await this.noAgents(workspaceId,signal);await this.captured.verify(signal);await this.directory(pending,signal);
 }
 async provision(workspaceId:string,signal:AbortSignal):Promise<OwnedWorkspaceRecord>{
  signal.throwIfAborted();check(!this.disposed&&!this.active&&!this.attempted.has(workspaceId)&&!this.records.has(workspaceId));
  const plan=this.plans.find(value=>value.workspaceId===workspaceId);check(plan);
  this.active=true;
  let finish!:()=>void;this.finished=new Promise<void>(resolve=>{finish=resolve;});
  const pending:Pending={created:false,added:false,deleted:false,completed:false};
  // Reserve before the first await; uncertain creation/addition is not replayable.
  this.attempted.set(workspaceId,pending);
  try{
   await this.captured.verify(signal);await this.parent(signal);
   await this.records.validateSourceRepositories(plan!.sources,signal);
   check(!this.disposed);pending.created=true;
   const created=await this.api.createEmpty(plan!.name,signal);check(created.status===201);
   const actual=Empty.parse(created.body).data;check(actual.id===workspaceId);
   const relative=path.relative(this.workspaceParent.path,actual.path);
   check(path.isAbsolute(actual.path)&&path.normalize(actual.path)===actual.path&&relative.length>0&&
    relative!=='..'&&!relative.startsWith(`..${path.sep}`)&&!path.isAbsolute(relative));
   const stat=await this.files.lstat(actual.path);check(stat.isDirectory()&&!stat.isSymbolicLink()&&await this.files.realpath(actual.path)===actual.path);
   const handle=await this.files.open(actual.path,constants.O_RDONLY|constants.O_NOFOLLOW);
   pending.directory={root:{path:actual.path,device:stat.dev,inode:stat.ino},handle};
   await this.directory(pending,signal);
   await this.captured.verify(signal);await this.empty(workspaceId,signal);
   const initial=WorkspaceEmptyCreationFact.parse({kind:'workspace-created-empty',...this.owner,
    identityKind:'native-agent-id',workspaceId,storeId:this.captured.storeId,storeGeneration:this.captured.storeGeneration,
    httpStatus:created.status,repositories:[],agentIds:[]});
   pending.initial=await this.evidence.retain(JSON.stringify(initial));
   // Revalidate after receipt I/O and immediately before the second mutation.
   await this.empty(workspaceId,signal);check(!this.disposed);
   pending.added=true;
   const added=await this.api.addRepositories(workspaceId,plan!.sources,signal);
   const addedPath=WorkspacePath.parse(added.body).data;
   check(addedPath.id===workspaceId&&addedPath.path===pending.directory!.root.path);await this.directory(pending,signal);
   await this.captured.verify(signal);await this.noAgents(workspaceId,signal);
   const record=await this.records.captureRepositoriesAdded(this.owner,workspaceId,plan!.sources,added,
    {storeId:this.captured.storeId,storeGeneration:this.captured.storeGeneration},pending.initial,this.evidence,signal,async abort=>{
     await this.captured.verify(abort);await this.directory(pending,abort);check(!this.disposed);
    });
   pending.completed=true;return record;
  }finally{this.active=false;finish();}
 }
 /** Cleanup can retry exact retained ownership after abort. It never adopts an
  * uncertain creation, a later actor, or repository topology from a listing. */
 async close():Promise<void>{
  this.disposed=true;
  if(this.closing)return this.closing;
  this.closing=(async()=>{
   await this.finished;
   const signal=new AbortController().signal;
   for(const [workspaceId,pending] of this.attempted){
    if(pending.deleted){if(pending.directory){await pending.directory.handle.close();pending.directory=undefined;}continue;}
    if(!pending.created){pending.deleted=true;continue;}
    check(pending.initial);
    await this.captured.verify(signal);
    if(pending.added){
     // A failed repository response has no authenticated topology to remove.
     check(this.records.has(workspaceId));await this.records.workspace(workspaceId,signal);
    }else await this.empty(workspaceId,signal);
    const current=await this.api.readWorkspace(workspaceId,signal);check(current.status===200);
    const actual=WorkspacePath.parse(current.body).data;
    check(actual.id===workspaceId&&actual.path===pending.directory!.root.path);
    await this.noAgents(workspaceId,signal);await this.captured.verify(signal);await this.directory(pending,signal);
    const response=await this.api.deleteWorkspace(workspaceId,signal);check(response.status===200&&
     z.object({success:z.literal(true)}).passthrough().parse(response.body));
    await this.captured.verify(signal);pending.deleted=true;
    if(pending.directory){await pending.directory.handle.close();pending.directory=undefined;}
   }
  })().finally(()=>{this.closing=undefined;});return this.closing;
 }
 /** Safe diagnostics expose receipt coordinates and uncertainty, never tokens
  * or transport bodies. Absence of a receipt is not proof of no resource. */
 diagnostics(){return [...this.attempted].map(([workspaceId,pending])=>({workspaceId,
  initialCreationReceipt:pending.initial??null,creationWriteAttempted:pending.created,repositoryWriteAttempted:pending.added,deleted:pending.deleted,
  complete:pending.deleted?!pending.directory:pending.completed&&!this.disposed}));}
}
