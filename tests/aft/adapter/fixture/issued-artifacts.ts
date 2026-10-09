import * as fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { redact } from '../protocol.js';
import type { OwnedRoot } from '../ownership.js';
import { FixtureError, type Artifact } from './lifecycle.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const digest=(value:Uint8Array)=>createHash('sha256').update(value).digest('hex');
const MAX_BYTES=4_000_000;
type Stamp={dev:number;ino:number;size:number;mtimeMs:number;ctimeMs:number};
type Issued={receipt:Readonly<Artifact>;stamp:Stamp};
/** Authenticating a private receipt is independent of its public fact schema.
 * Only this owning writer can register identities. Paths supplied by callers
 * cannot select a file, root, store or issuing fixture. */
export class IssuedArtifactReader {
 private readonly root:Readonly<OwnedRoot>;
 private readonly evidence:Readonly<OwnedRoot>;
 private readonly issued=new Map<string,Issued>();
 private readonly handles=new Set<fs.FileHandle>();
 private active?:Promise<void>;
 private disposed=false;
 private closing?:Promise<void>;
 constructor(root:OwnedRoot,evidence:OwnedRoot,enrollCleanup:(cleanup:()=>Promise<void>)=>void,
  private readonly files:typeof fs=fs,private readonly secrets:()=>readonly string[]=()=>[]){
  check(path.isAbsolute(root.path)&&path.normalize(root.path)===root.path&&evidence.path===path.join(root.path,'evidence'));
  this.root=Object.freeze({...root});this.evidence=Object.freeze({...evidence});
  enrollCleanup(()=>this.close());
 }
 private async verifyRoots(){
  for(const expected of [this.root,this.evidence]){
   const actual=await this.files.lstat(expected.path);
   check(actual.isDirectory()&&!actual.isSymbolicLink()&&actual.dev===expected.device&&actual.ino===expected.inode&&
    await this.files.realpath(expected.path)===expected.path);
  }
 }
 /** Bytes come directly from the owning artifact writer, not a report/YAML
  * receipt. Retain metadata only; no raw private value is cached here. */
 async remember(receipt:Artifact,bytes:Uint8Array){
  check(!this.issued.has(receipt.id)&&path.dirname(receipt.id)===this.evidence.path&&
   path.basename(receipt.id).endsWith('.json')&&receipt.mediaType==='application/json'&&receipt.redaction==='sanitized'&&
   Number.isSafeInteger(receipt.bytes)&&receipt.bytes===bytes.byteLength&&receipt.sha256===digest(bytes));
  await this.verifyRoots();const stat=await this.files.lstat(receipt.id);
  check(stat.isFile()&&!stat.isSymbolicLink()&&stat.nlink===1&&stat.size===receipt.bytes&&await this.files.realpath(receipt.id)===receipt.id);
  const stamp:Stamp={dev:stat.dev,ino:stat.ino,size:stat.size,mtimeMs:stat.mtimeMs,ctimeMs:stat.ctimeMs};
  await this.verifyRoots();check(!this.issued.has(receipt.id));
  this.issued.set(receipt.id,{receipt:Object.freeze({...receipt}),stamp:Object.freeze(stamp)});
 }
 async read(receipt:Artifact,signal:AbortSignal):Promise<string>{
  signal.throwIfAborted();check(!this.disposed&&!this.active);
  const issued=this.issued.get(receipt.id);check(issued);
  // Authenticate ALL receipt metadata and the authoritative size before any
  // filesystem resolution/open or caller-sized verification allocation.
  check(Object.keys(receipt).length===Object.keys(issued!.receipt).length&&
   Object.entries(issued!.receipt).every(([key,value])=>receipt[key as keyof Artifact]===value)&&
   issued!.receipt.bytes>0&&issued!.receipt.bytes<=MAX_BYTES);
  let finish!:()=>void;this.active=new Promise<void>(resolve=>{finish=resolve;});
  let handle:fs.FileHandle|undefined;
  const matches=(stat:Awaited<ReturnType<fs.FileHandle['stat']>>)=>stat.isFile()&&stat.nlink===1&&
   Object.entries(issued!.stamp).every(([key,value])=>stat[key as keyof typeof stat]===value);
  try{
   await this.verifyRoots();signal.throwIfAborted();check(!this.disposed);
   handle=await this.files.open(issued!.receipt.id,constants.O_RDONLY|constants.O_NOFOLLOW);this.handles.add(handle);
   check(!this.disposed);const before=await handle.stat();check(matches(before));
   const bytes=Buffer.alloc(issued!.receipt.bytes);let offset=0;
   while(offset<bytes.length){
    signal.throwIfAborted();check(!this.disposed);
    const read=await handle.read(bytes,offset,bytes.length-offset,offset);check(read.bytesRead>0);offset+=read.bytesRead;
   }
   const extra=await handle.read(Buffer.alloc(1),0,1,bytes.length),after=await handle.stat(),named=await this.files.lstat(issued!.receipt.id);
   check(extra.bytesRead===0&&matches(after)&&matches(named)&&!named.isSymbolicLink()&&digest(bytes)===issued!.receipt.sha256);
   await this.verifyRoots();check(await this.files.realpath(issued!.receipt.id)===issued!.receipt.id);
   signal.throwIfAborted();check(!this.disposed);
   const text=bytes.toString('utf8');check(Buffer.byteLength(text)===bytes.length&&redact(text,this.secrets())===text);
   return text;
  }finally{
   try{if(handle){await handle.close();this.handles.delete(handle);}}
   finally{this.active=undefined;finish();}
  }
 }
 /** Disposal waits an already-started open/read and retries exact descriptors
  * after close failures. Fresh cleanup does not depend on an aborted case. */
 async close():Promise<void>{
  this.disposed=true;if(this.closing)return this.closing;
  this.closing=(async()=>{
   await this.active;
   for(const handle of this.handles){await handle.close();this.handles.delete(handle);}
  })().finally(()=>{this.closing=undefined;});return this.closing;
 }
}
