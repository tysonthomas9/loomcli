import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { materializeRenderer } from './renderer-fixtures.test.js';
import { prepareRenderer, bindRenderer } from './renderer.js';
import type { RegisteredBuild } from './production.js';
import { sha256 } from '../protocol.js';
async function setup(){
 const root=await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname),'test-artifacts-'));
 const loom:RegisteredBuild={revision:{repository:'test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)},source:{root:path.join(root,'source'),entries:[]},build:{root:path.join(root,'build'),entries:[]}};
 await fs.mkdir(loom.source.root);await fs.mkdir(loom.build.root);await fs.mkdir(path.join(root,'receipt'));
 const renderer=await materializeRenderer(loom);return{root,loom,renderer,receipt:path.join(root,'receipt'),async cleanup(){await fs.rm(root,{recursive:true});}};
}
test('production renderer bind reads attested source/package/output bytes and records the actual owned target generation',async()=>{
 const r=await setup();try{const roots=new Map();const target=await bindRenderer(r.loom,'owned-lease',{targetId:'frontend',generation:'actual-generation',buildRoot:r.renderer.buildRoot},r.receipt,roots);
 const bytes=await fs.readFile(path.join(r.receipt,target.receipt.relativePath));assert.equal(target.receipt.sha256,await sha256(bytes));const receipt=JSON.parse(bytes.toString());assert.equal(receipt.fixtureLeaseId,'owned-lease');assert.equal(receipt.generation,'actual-generation');assert.equal(receipt.roots.source.path,r.renderer.sourceRoot);assert.equal(receipt.sources.length,6);assert.equal(receipt.packages.length,7);assert.equal(receipt.build.length,2);assert.equal(roots.size,4);
 }finally{await r.cleanup();}
});
for(const mismatch of ['missing','source','package','output','extra-chunk','symlink'])test(`renderer ${mismatch} fails before a runtime receipt can be created`,async()=>{
 const r=await setup();try{
 if(mismatch==='missing')await fs.unlink(path.join(r.loom.build.root,'renderer-build.json'));
 if(mismatch==='source')await fs.writeFile(path.join(r.renderer.sourceRoot,'package-lock.json'),'foreign');
 if(mismatch==='package')await fs.writeFile(path.join(r.renderer.installedRoot,'react/package.json'),'foreign');
 if(mismatch==='output')await fs.writeFile(path.join(r.renderer.buildRoot,'app.js'),'stale');
 if(mismatch==='extra-chunk')await fs.writeFile(path.join(r.renderer.buildRoot,'foreign.js'),'unattested');
 if(mismatch==='symlink'){await fs.unlink(path.join(r.renderer.buildRoot,'app.js'));await fs.symlink(path.join(r.renderer.buildRoot,'index.html'),path.join(r.renderer.buildRoot,'app.js'));}
 await assert.rejects(bindRenderer(r.loom,'owned-lease',{targetId:'frontend',generation:'actual',buildRoot:r.renderer.buildRoot},r.receipt,new Map()));assert.deepEqual(await fs.readdir(r.receipt),[]);
 }finally{await r.cleanup();}
});
test('renderer target using a different matching build is rejected; old receipt cannot be reused for a new lease',async()=>{
 const r=await setup();try{
 await assert.rejects(bindRenderer(r.loom,'owned-lease',{targetId:'frontend',generation:'actual',buildRoot:r.loom.build.root},r.receipt,new Map()));
 await bindRenderer(r.loom,'first',{targetId:'frontend',generation:'actual',buildRoot:r.renderer.buildRoot},r.receipt,new Map());
 await assert.rejects(bindRenderer(r.loom,'second',{targetId:'frontend',generation:'next',buildRoot:r.renderer.buildRoot},r.receipt,new Map()));
 assert.equal(JSON.parse(await fs.readFile(path.join(r.receipt,'renderer-target.json'),'utf8')).fixtureLeaseId,'first');
 }finally{await r.cleanup();}
});
test('renderer missing manifest entry cannot be trusted through matching bytes alone',async()=>{
 const r=await setup();try{r.loom.build.entries=r.loom.build.entries.filter(e=>!e.relativePath.endsWith('react/package.json'));await assert.rejects(prepareRenderer(r.loom));}finally{await r.cleanup();}
});
