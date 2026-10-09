import { spawn, type ChildProcess } from 'node:child_process';
import { z } from 'zod';
import path from 'node:path';
import { observeFilesystem } from '../filesystem.js';
import { FixtureError } from './lifecycle.js';
import type { Resource } from './lifecycle.js';
const check=(value:unknown)=>{if(!value)throw new FixtureError('ownership-mismatch');};
const Identity=z.object({pid:z.number().int().positive(),generation:z.string().min(1),executable:z.string().min(1),argvSha256:z.string().regex(/^[a-f0-9]{64}$/),
  parentPid:z.number().int().nonnegative(),configurationRoot:z.string(),state:z.enum(['running','exited'])}).strict();
export type RegisteredIdentity=z.infer<typeof Identity>;
export interface RegisteredProcessHandle {
 identity:RegisteredIdentity;
 inspect():Promise<RegisteredIdentity>;
 stop():Promise<void>;
 terminateGracefully?():Promise<void>;
 awaitExit?():Promise<void>;
 abandon():Promise<void>;
}
export interface RegisteredProcessPort { capture(pid:number):Promise<RegisteredProcessHandle>; }
/** Fixed installed module and attested interpreter are reviewed launcher inputs,
 * not operation data. The helper holds the exact kernel generation until close. */
export function createRegisteredProcessPort(pythonBinary:string,helperFile:string,spawnChild:typeof spawn=spawn):RegisteredProcessPort {
 return {async capture(pid){
  check(Number.isSafeInteger(pid)&&pid>0&&path.isAbsolute(pythonBinary)&&path.isAbsolute(helperFile));
  let child:ChildProcess;
  try{child=spawnChild(pythonBinary,[helperFile,String(pid)],{env:{PATH:path.dirname(pythonBinary)},shell:false,stdio:['pipe','pipe','ignore']});}
  catch{throw new FixtureError('observation-failed');}
  if(!child.stdin||!child.stdout){child.kill();throw new FixtureError('observation-failed');}
  let tail='',failed=false,exit:{code:number|null}|undefined,pending:{resolve:(value:RegisteredIdentity)=>void;reject:(error:FixtureError)=>void}|undefined;
  const fail=()=>{failed=true;pending?.reject(new FixtureError('observation-failed'));pending=undefined;};
  const response=()=>new Promise<RegisteredIdentity>((resolve,reject)=>{if(failed||pending)return reject(new FixtureError('observation-failed'));pending={resolve,reject};});
  const first=response();
  child.stdout!.on('data',(bytes:Buffer)=>{
    tail+=bytes.toString('utf8');if(Buffer.byteLength(tail)>65536){fail();child.kill();return;}
    let index;while((index=tail.indexOf('\n'))>=0){const line=tail.slice(0,index);tail=tail.slice(index+1);
      try{const raw=JSON.parse(line);
        if(z.object({error:z.enum(['cleanup-unverified','unsupported-capability'])}).strict().safeParse(raw).success&&pending){const saved=pending;pending=undefined;saved.reject(new FixtureError(raw.error==='unsupported-capability'?'unsupported-capability':'observation-failed'));continue;}
        const value=Identity.parse(raw);check(value.pid===pid&&pending);const saved=pending!;pending=undefined;saved.resolve(value);}
      catch{fail();child.kill();}
    }
  });
  child.once('error',fail);child.once('close',code=>{exit={code};fail();});child.stdin!.on('error',fail);
  const timeout=setTimeout(()=>{fail();child.kill();},15000);
  let initial:RegisteredIdentity;
  try{initial=await first;}catch(error){child.kill();throw error;}finally{clearTimeout(timeout);}
  const command=async(operation:'inspect'|'stop'|'terminate-gracefully'|'await-exit')=>{
    check(!failed&&!pending);const result=response();try{child.stdin!.write(JSON.stringify({operation})+'\n');}catch{fail();}
    const deadline=setTimeout(()=>{fail();child.kill();},20000);
    try{const value=await result;check(value.generation===initial.generation&&value.executable===initial.executable&&value.argvSha256===initial.argvSha256&&value.configurationRoot===initial.configurationRoot);return value;}
    finally{clearTimeout(deadline);}
  };
  let stopped=false;
  const close=async(operation:'close'|'abandon')=>{
    if(exit){check(exit.code===0);return;}
    const closed=new Promise<void>((resolve,reject)=>{child.once('close',code=>code===0?resolve():reject(new FixtureError('observation-failed')));});
    const deadline=setTimeout(()=>child.kill(),20000);
    try{child.stdin!.end(JSON.stringify({operation})+'\n');await closed;}finally{clearTimeout(deadline);}
  };
  return {identity:Object.freeze(initial),async awaitExit(){
    if(stopped)return;const value=await command('await-exit');check(value.state==='exited');
    await close('close');stopped=true;
  },async terminateGracefully(){
    if(stopped)return;const value=await command('terminate-gracefully');check(value.state==='exited');
    await close('close');stopped=true;
  },async inspect(){return stopped?{...initial,state:'exited'}:command('inspect');},async abandon(){await close('abandon');},async stop(){
    if(stopped)return;
    const value=await command('stop');check(value.state==='exited');
    await close('close');stopped=true;
  }};
 }};
}
export interface ProductProcessRegistration { id:string;pid:number;executable:string;configurationRoot:string;
 argvSha256?:string;parentPid?:number;parentPids?:readonly number[]; }
export class OwnedDescendants {
 private readonly handles=new Map<string,RegisteredProcessHandle>();
 constructor(private readonly port:RegisteredProcessPort,private readonly record:(resource:Resource)=>void){}
 async enroll(registration:ProductProcessRegistration){
  check(registration.id&&registration.pid>0&&registration.configurationRoot&&path.isAbsolute(registration.executable));
  const old=this.handles.get(registration.id);
  if(old){const current=await old.inspect();check(current.state==='running'&&current.pid===registration.pid&&current.executable===registration.executable&&current.configurationRoot===registration.configurationRoot&&
   current.generation===old.identity.generation&&current.argvSha256===old.identity.argvSha256&&
   (registration.argvSha256===undefined||current.argvSha256===registration.argvSha256)&&
   (registration.parentPid===undefined||current.parentPid===registration.parentPid)&&
   (registration.parentPids===undefined||registration.parentPids.filter(pid=>pid===current.parentPid).length===1));return current;}
  // The intent survives failures to inspect or pin a product-launched resource.
  this.record({id:registration.id,kind:'process',generation:`unverified:${registration.pid}`});
  const handle=await this.port.capture(registration.pid);const identity=Identity.parse(handle.identity);
  if(!(identity.state==='running'&&identity.pid===registration.pid&&identity.executable===registration.executable&&identity.configurationRoot===registration.configurationRoot&&
   (registration.argvSha256===undefined||identity.argvSha256===registration.argvSha256)&&
   (registration.parentPid===undefined||identity.parentPid===registration.parentPid)&&
   (registration.parentPids===undefined||registration.parentPids.filter(pid=>pid===identity.parentPid).length===1))){await handle.abandon();throw new FixtureError('ownership-mismatch');}
  this.handles.set(registration.id,handle);this.record({id:registration.id,kind:'process',generation:identity.generation});return identity;
 }
 async inspect(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);const current=Identity.parse(await handle!.inspect());check(['pid','generation','executable','argvSha256','configurationRoot'].every(key=>current[key as keyof RegisteredIdentity]===handle!.identity[key as keyof RegisteredIdentity]));return current;}
 async stop(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);await handle!.stop();}
 async terminateGracefully(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);
  if(!handle!.terminateGracefully)throw new FixtureError('unsupported-capability');await this.inspect(id,generation);await handle!.terminateGracefully();}
 async awaitExit(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);
  if(!handle!.awaitExit)throw new FixtureError('unsupported-capability');await this.inspect(id,generation);await handle!.awaitExit();}
 has(id:string){return this.handles.has(id);}
 initial(id:string){const handle=this.handles.get(id);check(handle);return handle!.identity;}
}
/** Only an observed fixed-TERM predecessor may have a product-created
 * successor. All maps here are private generation history, not lease authority. */
export interface ServiceSuccession {
 currentIds:Map<string,string>;
 terminated:Set<string>;
 pending:Map<string,{id:string;registration:string}>;
 nextId():string;
 parents():Promise<readonly {id:string;pid:number;generation:string}[]>;
 verifyParents(parents:readonly {id:string;pid:number;generation:string}[]):Promise<void>;
 argvSha256:string;
}
/** These are the product's two fixed service registration locations. Actor
 * workers, terminals and session harnesses have separate registration contracts;
 * this reader never represents their absence or discovers them by PID scans. */
export async function readRegisteredHostServices(configurationRoot:string,
 fleetExecutable:string,nativeExecutable:string,descendants:OwnedDescendants,retained:Map<string,unknown>,succession?:ServiceSuccession){
 const rows=[{path:'fleet-db/runtime.json',id:'registered-fleet-db',executable:fleetExecutable},
  {path:'agents-opencode/state/opencode/service.json',id:'registered-opencode-service',executable:nativeExecutable}];
 const observed:RegisteredIdentity[]=[];
 for(const row of rows){
  const before=await productRegistrationFile(configurationRoot,row.path);
  if(before===null)continue;
  const value=z.object({pid:z.number().int().positive(),url:z.string().min(1)}).passthrough().parse(before);
  const url=new URL(value.url);check(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname)&&!url.username&&!url.password);
  let identity:RegisteredIdentity,id=succession?.currentIds.get(row.id)??row.id;
  let successor:{predecessor:string;parents:readonly {id:string;pid:number;generation:string}[]}|undefined;
  if(retained.has(id)){
    const initial=descendants.initial(id);
    if(JSON.stringify(before)===JSON.stringify(retained.get(id))){
      check(initial.pid===value.pid);identity=await descendants.inspect(id,initial.generation);
    }else{
      check(row.id==='registered-opencode-service'&&succession?.terminated.has(id)&&
       (await descendants.inspect(id,initial.generation)).state==='exited');
      const parents=await succession!.parents();check(parents.length>0&&parents.length<=2);
      const predecessor=id,raw=JSON.stringify(before),pending=succession!.pending.get(predecessor);
      check(!pending||pending.registration===raw);
      const next=pending??{id:`${row.id}:successor-${succession!.nextId()}`,registration:raw};succession!.pending.set(predecessor,next);
      // Capture before resolving parent association. The exact kernel identity
      // chooses its one parent; never pick a parent from a PID/name listing.
      identity=await descendants.enroll({id:next.id,pid:value.pid,executable:row.executable,configurationRoot,argvSha256:succession!.argvSha256,parentPids:parents.map(parent=>parent.pid)});
      check(identity.generation!==initial.generation&&parents.filter(parent=>parent.pid===identity.parentPid).length===1);
      await succession!.verifyParents(parents);
      const after=await productRegistrationFile(configurationRoot,row.path);check(JSON.stringify(after)===raw);
      const current=await descendants.inspect(next.id,identity.generation);check(current.state==='running'&&current.parentPid===identity.parentPid);
      id=next.id;successor={predecessor,parents};
    }
  }else identity=await descendants.enroll({id,pid:value.pid,executable:row.executable,configurationRoot});
  const after=await productRegistrationFile(configurationRoot,row.path);
  check(JSON.stringify(after)===JSON.stringify(before));
  if(successor){await succession!.verifyParents(successor.parents);const current=await descendants.inspect(id,identity.generation);
    check(current.state==='running'&&current.parentPid===identity.parentPid);}
  retained.set(id,before);succession?.currentIds.set(row.id,id);
  if(successor){succession!.terminated.delete(successor.predecessor);succession!.pending.delete(successor.predecessor);}
  observed.push(identity);
 }
 return observed;
}
/** Absence here is only a local file fact. It never becomes an assertion that a
 * harness, session, event or descendant collection is empty. */
export async function productRegistrationFile(root:string,relativePath:string):Promise<unknown|null>{
 const read=await observeFilesystem({leaseId:'private-registration',rootId:'configuration',relativePaths:[relativePath],view:'bytes',maxBytes:4*1024*1024,maxEntries:1},root);
 const entry=read.entries[0]!;if(entry.kind==='missing')return null;
 check(entry.kind==='file'&&entry.contentBase64!==null);return JSON.parse(Buffer.from(entry.contentBase64!,'base64').toString('utf8'));
}
