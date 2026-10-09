import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { captureNativeStore } from './native-store.js';

const signal=()=>new AbortController().signal;
async function setup(){
 const directory=await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-store-'));
 const config=path.join(directory,'config');await fs.mkdir(config);
 const filename=path.join(config,'agents.db');
 // Byte files test physical binding only; no SQLite/product state is fabricated.
 await fs.writeFile(filename,'physical test bytes');const stamp=await fs.lstat(config);
 const cleanup:(()=>Promise<void>)[]=[];
 return {directory,config,filename,root:{path:config,device:stamp.dev,inode:stamp.ino},cleanup,
  enroll:(callback:()=>Promise<void>)=>cleanup.push(callback),remove:()=>fs.rm(directory,{recursive:true,force:true})};
}

test('retains the fixed file identity through legitimate in-place writes and closes after abort',async()=>{
 const s=await setup();try{
  const binding=await captureNativeStore(s.root,s.enroll,signal());assert.equal(s.cleanup.length,1);
  assert.equal(binding.root.path,s.filename);assert.match(binding.storeId,/^[a-f0-9]{64}$/);
  assert.match(binding.storeGeneration,/^[a-f0-9]{64}$/);
  const before={...binding.root};await fs.appendFile(s.filename,' changed bytes');await binding.verify(signal());
  assert.deepEqual(binding.root,before);assert.ok(Object.isFrozen(binding.root));
  const abort=new AbortController();abort.abort();await assert.rejects(binding.verify(abort.signal));
  await s.cleanup[0]!();await s.cleanup[0]!();await assert.rejects(binding.verify(signal()));
  assert.equal(await fs.readFile(s.filename,'utf8'),'physical test bytes changed bytes');
 }finally{await s.remove();}
});

test('rejects replacement and symlink without deleting either foreign file',async()=>{
 const s=await setup();try{
  const binding=await captureNativeStore(s.root,s.enroll,signal());
  const original=path.join(s.config,'original');await fs.rename(s.filename,original);
  await fs.writeFile(s.filename,'foreign replacement');await assert.rejects(binding.verify(signal()));
  await fs.unlink(s.filename);await fs.symlink(original,s.filename);await assert.rejects(binding.verify(signal()));
  await binding.close();assert.equal(await fs.readFile(original,'utf8'),'physical test bytes');
  assert.ok((await fs.lstat(s.filename)).isSymbolicLink());
 }finally{await s.remove();}
});

test('rejects replaced configuration directory even when the original database is moved into it',async()=>{
 const s=await setup();try{
  const binding=await captureNativeStore(s.root,s.enroll,signal());
  const old=s.config+'-old';await fs.rename(s.config,old);await fs.mkdir(s.config);
  await fs.rename(path.join(old,'agents.db'),s.filename);await assert.rejects(binding.verify(signal()));
  await binding.close();assert.equal(await fs.readFile(s.filename,'utf8'),'physical test bytes');
 }finally{await s.remove();}
});

test('enrolls before open and rejects a file swapped between lstat and open',async()=>{
 const s=await setup();let closes=0;try{
  const files={...fs,open:async(...args:Parameters<typeof fs.open>)=>{
   assert.equal(s.cleanup.length,1);await fs.rename(s.filename,s.filename+'-old');await fs.writeFile(s.filename,'replacement');
   const actual=await fs.open(...args);
   return new Proxy(actual,{get(target,key){if(key==='close')return async()=>{closes++;await target.close();};
    const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;}});
  }};
  await assert.rejects(captureNativeStore(s.root,s.enroll,signal(),files));assert.equal(closes,1);
  await s.cleanup[0]!();assert.equal(closes,1);assert.equal(await fs.readFile(s.filename,'utf8'),'replacement');
 }finally{await s.remove();}
});

test('retains a failed-close descriptor for exact cleanup retry',async()=>{
 const s=await setup();let closes=0;try{
  const files={...fs,open:async(...args:Parameters<typeof fs.open>)=>{
   const actual=await fs.open(...args);return new Proxy(actual,{get(target,key){
    if(key==='close')return async()=>{if(++closes===1)throw Error('injected close failure');await target.close();};
    const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;
   }});
  }};
  const binding=await captureNativeStore(s.root,s.enroll,signal(),files);
  await assert.rejects(binding.close(),/injected close failure/);await assert.rejects(binding.verify(signal()));
  await s.cleanup[0]!();assert.equal(closes,2);await binding.close();assert.equal(closes,2);
  await assert.rejects(binding.verify(signal()));
 }finally{await s.remove();}
});

test('failed acquisition after open retains cleanup when the first close fails',async()=>{
 const s=await setup();const abort=new AbortController();let closes=0;try{
  const files={...fs,open:async(...args:Parameters<typeof fs.open>)=>{
   const actual=await fs.open(...args);abort.abort();
   return new Proxy(actual,{get(target,key){
    if(key==='close')return async()=>{if(++closes===1)throw Error('injected partial-acquire close failure');await target.close();};
    const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;
   }});
  }};
  await assert.rejects(captureNativeStore(s.root,s.enroll,abort.signal,files),/partial-acquire close failure/);
  assert.equal(s.cleanup.length,1);assert.equal(closes,1);
  await s.cleanup[0]!();assert.equal(closes,2);await s.cleanup[0]!();assert.equal(closes,2);
  assert.equal(await fs.readFile(s.filename,'utf8'),'physical test bytes');
 }finally{await s.remove();}
});

test('concurrent cleanup awaits a pending open and closes its late descriptor exactly once',async()=>{
 const s=await setup();let entered!:()=>void,release!:()=>void,closes=0;try{
  const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  const files={...fs,open:async(...args:Parameters<typeof fs.open>)=>{
   const actual=await fs.open(...args);entered();await blocked;
   return new Proxy(actual,{get(target,key){if(key==='close')return async()=>{closes++;await target.close();};
    const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;}});
  }};
  const acquire=assert.rejects(captureNativeStore(s.root,s.enroll,signal(),files));await ready;
  const first=s.cleanup[0]!(),second=s.cleanup[0]!();release();await Promise.all([acquire,first,second]);
  assert.equal(closes,1);await s.cleanup[0]!();assert.equal(closes,1);
 }finally{release?.();await s.remove();}
});

test('a late descriptor close failure remains retryable after concurrent acquisition disposal',async()=>{
 const s=await setup();let entered!:()=>void,release!:()=>void,closes=0;try{
  const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  const files={...fs,open:async(...args:Parameters<typeof fs.open>)=>{
   const actual=await fs.open(...args);entered();await blocked;
   return new Proxy(actual,{get(target,key){if(key==='close')return async()=>{closes++;if(closes<=2)throw Error('injected concurrent close failure');await target.close();};
    const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;}});
  }};
  const acquire=assert.rejects(captureNativeStore(s.root,s.enroll,signal(),files));await ready;
  const cleanup=assert.rejects(s.cleanup[0]!(),/concurrent close failure/);release();await Promise.all([acquire,cleanup]);
  assert.equal(closes,2);await s.cleanup[0]!();assert.equal(closes,3);await s.cleanup[0]!();assert.equal(closes,3);
 }finally{release?.();await s.remove();}
});

test('rejects missing and foreign roots before open, leaving pre-enrolled cleanup safe',async()=>{
 const s=await setup();try{
  await assert.rejects(captureNativeStore({...s.root,inode:s.root.inode+1},s.enroll,signal()));await s.cleanup[0]!();
  await fs.unlink(s.filename);await assert.rejects(captureNativeStore(s.root,s.enroll,signal()));await s.cleanup[1]!();
  const abort=new AbortController();abort.abort();await assert.rejects(captureNativeStore(s.root,s.enroll,abort.signal));
  assert.equal(s.cleanup.length,2);
 }finally{await s.remove();}
});
