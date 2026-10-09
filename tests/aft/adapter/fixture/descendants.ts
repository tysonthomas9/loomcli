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
        if(z.object({error:z.literal('cleanup-unverified')}).strict().safeParse(raw).success&&pending){const saved=pending;pending=undefined;saved.reject(new FixtureError('observation-failed'));continue;}
        const value=Identity.parse(raw);check(value.pid===pid&&pending);const saved=pending!;pending=undefined;saved.resolve(value);}
      catch{fail();child.kill();}
    }
  });
  child.once('error',fail);child.once('close',code=>{exit={code};fail();});child.stdin!.on('error',fail);
  const timeout=setTimeout(()=>{fail();child.kill();},15000);
  let initial:RegisteredIdentity;
  try{initial=await first;}catch(error){child.kill();throw error;}finally{clearTimeout(timeout);}
  const command=async(operation:'inspect'|'stop')=>{
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
  return {identity:Object.freeze(initial),async inspect(){return stopped?{...initial,state:'exited'}:command('inspect');},async abandon(){await close('abandon');},async stop(){
    if(stopped)return;
    const value=await command('stop');check(value.state==='exited');
    await close('close');stopped=true;
  }};
 }};
}
export interface ProductProcessRegistration { id:string;pid:number;executable:string;configurationRoot:string; }
export class OwnedDescendants {
 private readonly handles=new Map<string,RegisteredProcessHandle>();
 constructor(private readonly port:RegisteredProcessPort,private readonly record:(resource:Resource)=>void){}
 async enroll(registration:ProductProcessRegistration){
  check(registration.id&&registration.pid>0&&registration.configurationRoot&&path.isAbsolute(registration.executable));
  const old=this.handles.get(registration.id);
  if(old){const current=await old.inspect();check(current.state==='running'&&current.pid===registration.pid&&current.executable===registration.executable&&current.configurationRoot===registration.configurationRoot);return current;}
  // The intent survives failures to inspect or pin a product-launched resource.
  this.record({id:registration.id,kind:'process',generation:`unverified:${registration.pid}`});
  const handle=await this.port.capture(registration.pid);const identity=Identity.parse(handle.identity);
  if(!(identity.pid===registration.pid&&identity.executable===registration.executable&&identity.configurationRoot===registration.configurationRoot)){await handle.abandon();throw new FixtureError('ownership-mismatch');}
  this.handles.set(registration.id,handle);this.record({id:registration.id,kind:'process',generation:identity.generation});return identity;
 }
 async inspect(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);const current=Identity.parse(await handle!.inspect());check(['pid','generation','executable','argvSha256','configurationRoot'].every(key=>current[key as keyof RegisteredIdentity]===handle!.identity[key as keyof RegisteredIdentity]));return current;}
 async stop(id:string,generation:string){const handle=this.handles.get(id);check(handle&&handle.identity.generation===generation);await handle!.stop();}
 has(id:string){return this.handles.has(id);}
 initial(id:string){const handle=this.handles.get(id);check(handle);return handle!.identity;}
}
/** These are the product's two fixed service registration locations. Actor
 * workers, terminals and session harnesses have separate registration contracts;
 * this reader never represents their absence or discovers them by PID scans. */
export async function readRegisteredHostServices(configurationRoot:string,
 fleetExecutable:string,nativeExecutable:string,descendants:OwnedDescendants,retained:Map<string,unknown>){
 const rows=[{path:'fleet-db/runtime.json',id:'registered-fleet-db',executable:fleetExecutable},
  {path:'agents-opencode/state/opencode/service.json',id:'registered-opencode-service',executable:nativeExecutable}];
 const observed:RegisteredIdentity[]=[];
 for(const row of rows){
  const before=await productRegistrationFile(configurationRoot,row.path);
  if(before===null)continue;
  const value=z.object({pid:z.number().int().positive(),url:z.string().min(1)}).passthrough().parse(before);
  const url=new URL(value.url);check(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname)&&!url.username&&!url.password);
  let identity:RegisteredIdentity;
  if(retained.has(row.id)){
    check(JSON.stringify(before)===JSON.stringify(retained.get(row.id)));
    const initial=descendants.initial(row.id);check(initial.pid===value.pid);
    identity=await descendants.inspect(row.id,initial.generation);
  }else identity=await descendants.enroll({id:row.id,pid:value.pid,executable:row.executable,configurationRoot});
  const after=await productRegistrationFile(configurationRoot,row.path);
  check(JSON.stringify(after)===JSON.stringify(before));retained.set(row.id,before);observed.push(identity);
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
