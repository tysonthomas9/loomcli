import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { z } from 'zod';
import type { OwnedRoot } from '../ownership.js';
import { FixtureError } from './lifecycle.js';
import { OwnedDescendants, productRegistrationFile, type RegisteredIdentity } from './descendants.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const text=z.string().min(1).max(4096);
const time=z.string().datetime({offset:true});
const Sidecar=z.object({pid:z.number().int().positive(),started_at:time,cwd:text,socket:text});
const Row=z.object({worktree:text,role:text,pid:z.number().int().nonnegative(),
 status:z.enum(['running','starting','stopped','failed','blocked']),worktree_path:text.optional(),
 current_backend:text.optional(),epic_id:text.optional(),last_start:time.optional()});
const State=z.object({pid:z.number().int().positive(),started_at:time,agents:z.array(Row).max(1000)});
export interface WorkerParent {id:string;identity:RegisteredIdentity;}
export interface OwnedWorkerFact {id:string;generation:string;kind:'worker';identityKind:'legacy-agent-name';
 workspaceId:string;agentId:string;sessionName:null;}
export interface WorkerCoordinates {
 configurationRoot:OwnedRoot;runtimeRoot:OwnedRoot;workspaceId:string;daemonCwd:string;loomExecutable:string;
}
/** Code-owned fixed ports. Actor verification must use canonical retained
 * workspace membership and the actual physical product worktree diagnostic. */
export interface WorkerPorts {
 parent(signal:AbortSignal):Promise<WorkerParent>;
 verifyParent(parent:WorkerParent,signal:AbortSignal):Promise<void>;
 verifyActor(name:string,worktree:string,signal:AbortSignal):Promise<void>;
 nextId():string;
 stop?(fact:Readonly<OwnedWorkerFact>,serveGeneration:string,signal:AbortSignal):Promise<{status:number;body:unknown}>;
}
interface RetainedWorker {
 fact:Readonly<OwnedWorkerFact>;row:Readonly<z.infer<typeof Row>>;parent:Readonly<WorkerParent>;
}
/** Narrow builtin worker registration. No enumeration by PID/name, process
 * launch, force-stop actor, terminal exit or native-session association. */
export class RegisteredBuiltinWorkers {
 private active=false;
 private readonly current=new Map<string,string>();
 private readonly retained=new Map<string,Readonly<RetainedWorker>>();
 private readonly pending=new Map<string,{id:string;row:z.infer<typeof Row>}>();
 private readonly stopAttempts=new Set<string>();
 constructor(private readonly coordinates:WorkerCoordinates,private readonly ports:WorkerPorts,
  private readonly descendants:OwnedDescendants,private readonly files:typeof fs=fs){
  this.coordinates=Object.freeze({...coordinates,configurationRoot:Object.freeze({...coordinates.configurationRoot}),
   runtimeRoot:Object.freeze({...coordinates.runtimeRoot})});
 }
 private async verifyRoot(root:OwnedRoot){
  const actual=await this.files.lstat(root.path);
  check(path.isAbsolute(root.path)&&path.normalize(root.path)===root.path&&actual.isDirectory()&&!actual.isSymbolicLink()&&
   actual.dev===root.device&&actual.ino===root.inode&&await this.files.realpath(root.path)===root.path);
 }
 private async read(root:OwnedRoot,relative:string){
  await this.verifyRoot(root);const value=await productRegistrationFile(root.path,relative);await this.verifyRoot(root);check(value!==null);return value;
 }
 async refresh(signal:AbortSignal):Promise<readonly OwnedWorkerFact[]>{
  signal.throwIfAborted();check(!this.active);this.active=true;
  try{return await this.refreshChecked(signal);}finally{this.active=false;}
 }
 /** Reads an already captured generation, including an exited predecessor.
  * Current sidecars cannot confer authority, select a successor or erase this
  * history. The owning Host reservation must also exclude resource cleanup. */
 async readRetained(id:string,generation:string,signal:AbortSignal){
  signal.throwIfAborted();check(!this.active);this.active=true;
  try{
   const record=this.retained.get(id);check(record&&record.fact.generation===generation);
   const {fact,row,parent}=record!;
   check(row.worktree_path&&parent.identity.state==='running');
   await this.verifyRoot(this.coordinates.configurationRoot);await this.verifyRoot(this.coordinates.runtimeRoot);
   await this.ports.verifyParent(parent,signal);
   const before=await this.descendants.inspect(id,generation);
   check(before.parentPid===parent.identity.pid&&before.executable===this.coordinates.loomExecutable&&
    before.configurationRoot===this.coordinates.configurationRoot.path);
   signal.throwIfAborted();await this.ports.verifyActor(fact.agentId,row.worktree_path!,signal);
   await this.ports.verifyParent(parent,signal);
   const after=await this.descendants.inspect(id,generation);
   check(after.parentPid===parent.identity.pid&&!(before.state==='exited'&&after.state!=='exited'));
   await this.verifyRoot(this.coordinates.configurationRoot);await this.verifyRoot(this.coordinates.runtimeRoot);
   await this.ports.verifyParent(parent,signal);signal.throwIfAborted();
   check(this.retained.get(id)===record);
   return Object.freeze({fact,before:Object.freeze(before),after:Object.freeze(after)});
  }finally{this.active=false;}
 }
 /** The product API actor and the kernel exit observation are separate facts.
  * No force-cleanup or saved-command restart substitutes for either one. */
 async stop(id:string,generation:string,serveGeneration:string,signal:AbortSignal){
  signal.throwIfAborted();check(!this.active&&serveGeneration.length>0);this.active=true;
  try{
   if(!this.ports.stop)throw new FixtureError('unsupported-capability');
   const key=JSON.stringify([id,generation]);check(!this.stopAttempts.has(key));
   const facts=await this.refreshChecked(signal),fact=facts.find(value=>value.id===id&&value.generation===generation);
   check(fact);this.descendants.requireExitObservation(id,generation);
   const parent=await this.ports.parent(signal);await this.ports.verifyParent(parent,signal);
   check(parent.identity.state==='running'&&(await this.descendants.inspect(id,generation)).parentPid===parent.identity.pid);
   signal.throwIfAborted();this.stopAttempts.add(key);
   const response=await this.ports.stop(fact!,serveGeneration,signal);
   check(Number.isInteger(response.status)&&response.status>=200&&response.status<300);
   await this.ports.verifyParent(parent,signal);signal.throwIfAborted();
   await this.descendants.awaitExit(id,generation);
   signal.throwIfAborted();check((await this.descendants.inspect(id,generation)).state==='exited');
   await this.ports.verifyParent(parent,signal);
   const row=this.retained.get(id);check(this.current.get(fact!.agentId)===id&&row?.fact.generation===generation&&row.row.worktree_path);
   await this.ports.verifyActor(fact!.agentId,row!.row.worktree_path!,signal);
   await this.ports.verifyParent(parent,signal);
   return {response,transition:{beforeGeneration:generation,afterGeneration:null,affectedIds:[id],complete:true as const}};
  }finally{this.active=false;}
 }
 private async refreshChecked(signal:AbortSignal){
  const c=this.coordinates;
  check(/^[A-Za-z0-9_-]+$/.test(c.workspaceId)&&path.isAbsolute(c.loomExecutable)&&path.isAbsolute(c.daemonCwd));
  const parent=await this.ports.parent(signal);
  check(parent.identity.state==='running'&&parent.identity.executable===c.loomExecutable&&parent.identity.configurationRoot===c.configurationRoot.path);
  await this.ports.verifyParent(parent,signal);
  const sidecarPath=`workspaces/${c.workspaceId}/daemon.pid`;
  const sidecar=Sidecar.parse(await this.read(c.configurationRoot,sidecarPath));
  check(sidecar.pid===parent.identity.pid&&sidecar.cwd===c.daemonCwd&&path.basename(sidecar.socket)==='daemon.sock');
  const statePath=path.join(path.dirname(sidecar.socket),'daemon-agents.json');
  const relative=path.relative(c.runtimeRoot.path,statePath);
  check(path.isAbsolute(sidecar.socket)&&path.normalize(sidecar.socket)===sidecar.socket&&relative.length>0&&
   !relative.startsWith('..'+path.sep)&&relative!=='..'&&!path.isAbsolute(relative));
  const readState=async()=>{
   const state=State.parse(await this.read(c.runtimeRoot,relative));
   check(state.pid===parent.identity.pid&&new Set(state.agents.map(row=>row.worktree)).size===state.agents.length);
   // Narrowing discards private lease/fencing fields rather than retaining them
   // in process facts or diagnostics. This list is not an exit/absence oracle.
   return state;
  };
  const before=await readState();const observed:OwnedWorkerFact[]=[];
  for(const row of before.agents){
   signal.throwIfAborted();
   if(row.status!=='running'){
    check(row.pid===0);continue;
   }
   if(!['plan','task'].includes(row.role))throw new FixtureError('unsupported-capability');
   check(row.pid>0&&row.worktree_path&&row.last_start);
   await this.ports.verifyActor(row.worktree,row.worktree_path!,signal);
   await this.ports.verifyParent(parent,signal);
   const argv=[c.loomExecutable,row.role,row.worktree_path!,'--auto','--daemon-mode'];
   if(row.current_backend)argv.push('--backend',row.current_backend);
   if(row.epic_id)argv.push('--parent',row.epic_id);
   const argvSha256=createHash('sha256').update(Buffer.from(argv.join('\0')+'\0')).digest('hex');
   const currentId=this.current.get(row.worktree),previous=currentId===undefined?undefined:this.retained.get(currentId);let id:string;
   if(previous&&isDeepStrictEqual(previous.row,row)){
    check((await this.descendants.inspect(previous.fact.id,previous.fact.generation)).state==='running');id=previous.fact.id;
   }else{
    if(previous){check((await this.descendants.inspect(previous.fact.id,previous.fact.generation)).state==='exited'&&previous.row.last_start!==row.last_start);}
    const intent=this.pending.get(row.worktree);
    check(!intent||isDeepStrictEqual(intent.row,row));
    check(this.retained.size<1000);
    id=intent?.id??`registered-worker-${this.ports.nextId()}`;
    if(!intent)this.pending.set(row.worktree,{id,row});
   }
   const identity=await this.descendants.enroll({id,pid:row.pid,executable:c.loomExecutable,configurationRoot:c.configurationRoot.path,
    argvSha256,parentPid:parent.identity.pid});
   check(identity.generation!==previous?.fact.generation||id===previous?.fact.id);
   await this.ports.verifyParent(parent,signal);
   await this.ports.verifyActor(row.worktree,row.worktree_path!,signal);
   const reread=await readState();check(isDeepStrictEqual(reread,before));
   check((await this.descendants.inspect(id,identity.generation)).state==='running');
   const fact=Object.freeze({id,generation:identity.generation,kind:'worker' as const,identityKind:'legacy-agent-name' as const,
    workspaceId:c.workspaceId,agentId:row.worktree,sessionName:null});
   const retained=this.retained.get(id);
   if(retained)check(retained.parent.id===parent.id&&isDeepStrictEqual(retained.parent.identity,parent.identity));
   else this.retained.set(id,Object.freeze({fact,row:Object.freeze({...row}),
    parent:Object.freeze({id:parent.id,identity:Object.freeze({...parent.identity})})}));
   this.current.set(row.worktree,id);this.pending.delete(row.worktree);
   observed.push(fact);
  }
  check(isDeepStrictEqual(await readState(),before));
  check(isDeepStrictEqual(Sidecar.parse(await this.read(c.configurationRoot,sidecarPath)),sidecar));
  await this.ports.verifyParent(parent,signal);return Object.freeze(observed.map(value=>Object.freeze(value)));
 }
}
