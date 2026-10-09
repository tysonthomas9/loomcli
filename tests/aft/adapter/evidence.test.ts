import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createEvidenceStore } from './evidence.js';

async function setup(t:{after(fn:()=>Promise<void>):void}) {
  const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-bounded-evidence-')));
  t.after(()=>rm(root,{recursive:true}));
  return {root,store:await createEvidenceStore(root)};
}
test('bounded evidence accepts the exact retained boundary and preserves ordinary resolve',async t=>{
  const {store}=await setup(t),receipt=await store.retain('x'.repeat(4_000_000));
  assert.equal(await store.resolveBounded(receipt,4_000_000),await store.resolve(receipt.id));
});
test('bounded evidence rejects authoritative one-over bytes before a verification allocation',async t=>{
  const {store}=await setup(t),receipt=await store.retain('x'.repeat(4_000_001));
  const original=Buffer.alloc;let oversized=0;
  Buffer.alloc=((size:number,...args:unknown[])=>{if(size>4_000_000)oversized++;return Reflect.apply(original,Buffer,[size,...args]);}) as typeof Buffer.alloc;
  try {
    await assert.rejects(store.resolveBounded(receipt,4_000_000),/exceeds the read bound/);
    await assert.rejects(store.resolveBounded({...receipt,bytes:1},4_000_000),/exceeds the read bound/);
  } finally {Buffer.alloc=original;}
  assert.equal(oversized,0);
});
test('bounded resolution authenticates every retained receipt field and denies unknown identity',async t=>{
  const {store}=await setup(t),receipt=await store.retain('hello');
  for(const changed of [{...receipt,bytes:1},{...receipt,sha256:'0'.repeat(64)},
    {...receipt,mediaType:'text/plain'}, {...receipt,redaction:'public' as const}])
    await assert.rejects(store.resolveBounded(changed,4_000_000));
  await assert.rejects(store.resolveBounded({...receipt,id:'foreign'},4_000_000),/unknown/);
  for(const bound of [0,-1,Infinity,1.5])await assert.rejects(store.resolveBounded(receipt,bound),/bound is invalid/);
});
test('bounded resolution still verifies actual bytes rather than trusting retained metadata',async t=>{
  const {root,store}=await setup(t),receipt=await store.retain('hello');
  await writeFile(path.join(root,receipt.id),'world');
  await assert.rejects(store.resolveBounded(receipt,4_000_000),/changed/);
});
