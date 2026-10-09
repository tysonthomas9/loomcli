import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {IssuedArtifactReader} from './issued-artifacts.js';
function deferred(){let resolve!:()=>void;const promise=new Promise<void>(r=>resolve=r);return {promise,resolve};}
async function setup(blockClose=false){
 const root=await fs.mkdtemp(path.resolve('fixture/test-artifacts-parent-'));const evidence=path.join(root,'evidence');await fs.mkdir(evidence);
 const rs=await fs.stat(root), es=await fs.stat(evidence);const started=deferred(),release=deferred();let block=blockClose;
 const files={...fs,async open(...args:Parameters<typeof fs.open>){const h=await fs.open(...args),close=h.close.bind(h);h.close=async()=>{if(block){block=false;started.resolve();await release.promise;}await close();};return h;}};
 const reader=new IssuedArtifactReader({path:root,device:rs.dev,inode:rs.ino},{path:evidence,device:es.dev,inode:es.ino},()=>{},files);
 const issue=async(bytes:Buffer)=>{const receipt={id:path.join(evidence,'issued.json'),sha256:createHash('sha256').update(bytes).digest('hex'),bytes:bytes.length,mediaType:'application/json',redaction:'sanitized'} as const;await fs.writeFile(receipt.id,bytes);await reader.remember(receipt,bytes);return receipt;};
 return {reader,issue,started,release,async cleanup(){release.resolve();await reader.close();await fs.rm(root,{recursive:true,force:true});}};
}
test('invalid three-byte truncated four-byte UTF8 sequence must not be silently repaired',async()=>{
 const s=await setup();try{const bytes=Buffer.concat([Buffer.from('{"value":"'),Buffer.from([0xf0,0x90,0x80]),Buffer.from('"}')]);const r=await s.issue(bytes);await assert.rejects(s.reader.read(r,new AbortController().signal));}finally{await s.cleanup();}
});
for(const mode of ['abort','dispose'])test(`read must reject ${mode} while descriptor close is pending`,async()=>{
 const s=await setup(true),c=new AbortController();try{const r=await s.issue(Buffer.from('{"value":"actual"}'));const read=s.reader.read(r,c.signal);const rejection=assert.rejects(read);await s.started.promise;const disposed=mode==='dispose'?s.reader.close():undefined;if(mode==='abort')c.abort();s.release.resolve();await rejection;await disposed;}finally{await s.cleanup();}
});
test('ordinary valid issued data returns unchanged',async()=>{const s=await setup();try{const text='{"value":"actual"}';const r=await s.issue(Buffer.from(text));assert.equal(await s.reader.read(r,new AbortController().signal),text);}finally{await s.cleanup();}});
