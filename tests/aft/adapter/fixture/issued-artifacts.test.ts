import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { IssuedArtifactReader } from './issued-artifacts.js';
import type { Artifact } from './lifecycle.js';

const signal=()=>new AbortController().signal;
const sha=(bytes:Uint8Array)=>createHash('sha256').update(bytes).digest('hex');
function deferred(){let resolve!:()=>void;const promise=new Promise<void>(value=>{resolve=value;});return {promise,resolve};}
async function setup(){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-issued-'));const evidence=path.join(root,'evidence');await fs.mkdir(evidence);
 const rootStat=await fs.lstat(root),evidenceStat=await fs.lstat(evidence),cleanups:(()=>Promise<void>)[]=[];
 let opens=0,closes=0,serial=0,failClose=false;
 let beforeOpen:(()=>Promise<void>)|undefined,afterRead:(()=>Promise<void>)|undefined;
 const handles:fs.FileHandle[]=[];
 const files={...fs,async open(...args:Parameters<typeof fs.open>){
  opens++;await beforeOpen?.();const handle=await fs.open(...args),read=handle.read.bind(handle),close=handle.close.bind(handle);
  handle.read=(async(...readArgs:unknown[])=>{const result=await (read as (...args:unknown[])=>Promise<unknown>)(...readArgs);await afterRead?.();return result;}) as typeof handle.read;
  handle.close=async()=>{closes++;if(failClose){failClose=false;throw Error('injected close failure');}await close();};
  handles.push(handle);return handle;
 }};
 const reader=new IssuedArtifactReader({path:root,device:rootStat.dev,inode:rootStat.ino},
  {path:evidence,device:evidenceStat.dev,inode:evidenceStat.ino},cleanup=>cleanups.push(cleanup),files,()=>['private-owned-secret']);
 const issue=async(value:unknown)=>{
  const bytes=Buffer.from(JSON.stringify(value)),id=path.join(evidence,`issued-${++serial}.json`);
  const receipt:Artifact={id,sha256:sha(bytes),bytes:bytes.length,mediaType:'application/json',redaction:'sanitized'};
  await fs.writeFile(id,bytes,{mode:0o600,flag:'wx'});await reader.remember(receipt,bytes);return receipt;
 };
 return {root,evidence,reader,issue,cleanups,opens:()=>opens,closes:()=>closes,
  beforeOpen:(hook:()=>Promise<void>)=>{beforeOpen=hook;},afterRead:(hook:()=>Promise<void>)=>{afterRead=hook;},failClose:()=>{failClose=true;},
  async remove(){for(const handle of handles)await handle.close();await fs.rm(root,{recursive:true,force:true});}};
}

test('only exact writer-issued receipt metadata selects retained private bytes',async()=>{
 const s=await setup();try{
  const value={kind:'observe',value:{operation:'compose-restarted',actualPid:41},images:{loom:'source-image'}};
  const receipt=await s.issue(value);assert.deepEqual(JSON.parse(await s.reader.read(receipt,signal())),value);
  assert.equal(s.opens(),1);assert.equal(s.closes(),1);assert.equal(s.cleanups.length,1);
  for(const change of [{id:path.join(s.evidence,'unknown.json')},{sha256:'0'.repeat(64)},{bytes:receipt.bytes-1},
   {mediaType:'text/plain'},{redaction:'raw'},{unissuedSource:'foreign'}]){
   await assert.rejects(s.reader.read({...receipt,...change} as Artifact,signal()));assert.equal(s.opens(),1);
  }
 }finally{await s.remove();}
});

test('authoritative issued size, not understated caller bytes, bounds verification before open',async()=>{
 const s=await setup();try{
  const receipt=await s.issue({value:'x'.repeat(4_000_001)});
  await assert.rejects(s.reader.read(receipt,signal()));
  await assert.rejects(s.reader.read({...receipt,bytes:2},signal()));assert.equal(s.opens(),0);
 }finally{await s.remove();}
});

test('foreign issuer, replacement evidence root and unissued matching file never acquire receipt authority',async()=>{
 const a=await setup(),b=await setup();try{
  const receipt=await a.issue({value:'actual'});await assert.rejects(b.reader.read(receipt,signal()));assert.equal(b.opens(),0);
  const clone={...receipt,id:path.join(a.evidence,'copy.json')};await fs.copyFile(receipt.id,clone.id);
  await assert.rejects(a.reader.read(clone,signal()));assert.equal(a.opens(),0);
  await fs.rename(a.evidence,a.evidence+'.retained');await fs.mkdir(a.evidence);await fs.copyFile(path.join(a.evidence+'.retained',path.basename(receipt.id)),receipt.id);
  await assert.rejects(a.reader.read(receipt,signal()));assert.equal(a.opens(),0);
 }finally{await a.remove();await b.remove();}
});

test('symlink, hardlink, same-path replacement, tampering and truncation reject instead of returning facts',async()=>{
 for(const change of ['symlink','hardlink','replacement','tamper','truncate']){
  const s=await setup();try{
   const receipt=await s.issue({value:'original'});
   if(change==='hardlink')await fs.link(receipt.id,path.join(s.evidence,'extra.json'));
   else if(change==='tamper')await fs.writeFile(receipt.id,JSON.stringify({value:'tampered'}));
   else if(change==='truncate')await fs.truncate(receipt.id,2);
   else{await fs.rename(receipt.id,receipt.id+'.retained');
    if(change==='symlink')await fs.symlink(receipt.id+'.retained',receipt.id);else await fs.copyFile(receipt.id+'.retained',receipt.id);}
   await assert.rejects(s.reader.read(receipt,signal()));
  }finally{await s.remove();}
 }
});

test('replacement during read fails before bytes are exposed and closes the original descriptor',async()=>{
 const s=await setup();try{
  const receipt=await s.issue({value:'original'});let changed=false;
  s.afterRead(async()=>{if(!changed){changed=true;await fs.rename(receipt.id,receipt.id+'.retained');await fs.copyFile(receipt.id+'.retained',receipt.id);}});
  await assert.rejects(s.reader.read(receipt,signal()));assert.equal(s.closes(),1);
 }finally{await s.remove();}
});

test('pending open disposal waits acquisition and closes its exact late descriptor once',async()=>{
 const s=await setup(),started=deferred(),release=deferred();try{
  const receipt=await s.issue({value:'actual'});
  s.beforeOpen(async()=>{started.resolve();await release.promise;});
  const rejected=assert.rejects(s.reader.read(receipt,signal()));await started.promise;
  const disposed=s.cleanups[0]!();release.resolve();await rejected;await disposed;await s.cleanups[0]!();
  assert.equal(s.closes(),1);await assert.rejects(s.reader.read(receipt,signal()));assert.equal(s.opens(),1);
 }finally{release.resolve();await s.remove();}
});

test('failed read close retains exact descriptor for cleanup retry after abort',async()=>{
 const s=await setup();try{
  const receipt=await s.issue({value:'actual'});s.failClose();await assert.rejects(s.reader.read(receipt,signal()));
  assert.equal(s.closes(),1);await s.cleanups[0]!();assert.equal(s.closes(),2);await s.cleanups[0]!();assert.equal(s.closes(),2);
  await assert.rejects(s.reader.read(receipt,AbortSignal.abort()));assert.equal(s.opens(),1);
 }finally{await s.remove();}
});

test('private token-bearing or invalid UTF8 artifacts do not become sanitized public facts',async()=>{
 const s=await setup();try{
  for(const value of [{value:'private-owned-secret'},{value:'Bearer actual-credential'}]){
   const receipt=await s.issue(value);await assert.rejects(s.reader.read(receipt,signal()));
  }
  const bytes=Buffer.from([0xff,0xfe]),receipt:Artifact={id:path.join(s.evidence,'invalid.json'),sha256:sha(bytes),bytes:2,mediaType:'application/json',redaction:'sanitized'};
  await fs.writeFile(receipt.id,bytes);await s.reader.remember(receipt,bytes);await assert.rejects(s.reader.read(receipt,signal()));
 }finally{await s.remove();}
});

test('valid replacement characters and BOM data remain byte-exact with fatal UTF8 decoding',async()=>{
 const s=await setup();try{
  const text=JSON.stringify({value:'actual \uFFFD \uFEFF data'}),receipt=await s.issue({value:'actual \uFFFD \uFEFF data'});
  assert.equal(await s.reader.read(receipt,signal()),text);
 }finally{await s.remove();}
});
