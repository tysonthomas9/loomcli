import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { materializeRenderer } from './renderer-fixtures.test.js';
import { prepareRenderer, bindRenderer, writeRendererBuildReceipt, rendererSources, rendererPackages } from './renderer.js';
import type { RegisteredBuild } from './production.js';
import { sha256 } from '../protocol.js';
import { RendererSourceBuildReceipt, RendererBuildReceipt } from '../renderer-contract.js';
import { rendererBuildClosure, measureFixtureProjection } from '../renderer-target.js';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { putFixture, type OwnedFixture } from '../ownership.js';
import { createEvidenceStore, putEvidenceStore } from '../evidence.js';
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
test('production post-build writer reads a nested output closure and never replaces an existing receipt',async()=>{
 const r=await setup();try{
 await fs.unlink(path.join(r.loom.build.root,'renderer-build.json'));
 // Actual reviewed bytes enable the root production validator. This remains a
 // deterministic file/ownership test, with no frontend compilation or serving.
 const adapterRoot=path.resolve(path.dirname(new URL(import.meta.url).pathname),'..');
 for(const [files,origin,destination,manifest,prefix] of [
  [rendererSources,path.resolve(adapterRoot,'../../../internal/webui/frontend'),r.renderer.sourceRoot,r.loom.source,'internal/webui/frontend'],
  [rendererPackages,path.join(adapterRoot,'node_modules'),r.renderer.installedRoot,r.loom.build,'renderer-packages'],
 ] as const)for(const file of files){await fs.copyFile(path.join(origin,file),path.join(destination,file));manifest.entries.find(e=>e.relativePath===`${prefix}/${file}`)!.sha256=await sha256(await fs.readFile(path.join(destination,file)));}
 r.loom.revision.sourceManifestSha256=await sha256([...r.loom.source.entries].sort((a,b)=>a.relativePath<b.relativePath?-1:1).map(e=>`${e.sha256}  ${e.relativePath}\n`).join(''));
 await fs.mkdir(path.join(r.renderer.buildRoot,'assets'));const chunk=path.join(r.renderer.buildRoot,'assets/chunk.js');await fs.writeFile(chunk,'actual chunk bytes');
 for(const suffix of ['svg','woff2','png'])await fs.writeFile(path.join(r.renderer.buildRoot,`assets/actual.${suffix}`),Buffer.from([0,255,1,2,3]));
 const input={...r.renderer,receiptRoot:r.loom.build.root,sourceManifestSha256:r.loom.revision.sourceManifestSha256};
 const result=await writeRendererBuildReceipt(input);assert.equal(result.receipt.build.length,6);
 assert.equal(result.receipt.build.find(f=>f.relativePath==='assets/chunk.js')?.sha256,await sha256(await fs.readFile(chunk)));
 const filename=path.join(r.loom.build.root,result.relativePath),before=await fs.readFile(filename);assert.equal(result.sha256,await sha256(before));
 assert.deepEqual(RendererSourceBuildReceipt.parse(JSON.parse(before.toString())),result.receipt);
 assert.deepEqual(await rendererBuildClosure(r.renderer.buildRoot),result.receipt.build);
 for(const file of result.receipt.build)if(!r.loom.build.entries.some(e=>e.relativePath===`renderer-dist/${file.relativePath}`))r.loom.build.entries.push({...file,relativePath:`renderer-dist/${file.relativePath}`});
 r.loom.build.entries.find(e=>e.relativePath===result.relativePath)!.sha256=result.sha256;
 await prepareRenderer(r.loom);
 const roots:OwnedFixture['roots']=new Map();const bound=await bindRenderer(r.loom,'actual-lease',{targetId:'served',generation:'owned-generation',buildRoot:r.renderer.buildRoot},r.receipt,roots);
 const targetReceipt=RendererBuildReceipt.parse(JSON.parse(await fs.readFile(path.join(r.receipt,bound.receipt.relativePath),'utf8')));assert.deepEqual(targetReceipt.build,result.receipt.build);
 const context=createCapabilityContext({file:'renderer-writer.test.yaml',line:1},new CapabilityRegistry());
 putEvidenceStore(context,await createEvidenceStore(r.receipt));const unavailable=async():Promise<never>=>{throw new Error('No product transport used');};
 putFixture(context,{leaseId:'actual-lease',runId:context.runId,suiteId:context.suiteId,scope:context.scope,caseId:context.caseId,profile:'deterministic-renderer-contract',workspaceId:'workspace',repo:r.renderer.sourceRoot,
  expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',roots,agents:new Map(),secrets:[],rendererTarget:bound,readApi:unavailable,readFiles:unavailable,resolveAgent:unavailable,verify:async()=>{},dispose:async()=>{}});
 const measured=await measureFixtureProjection(context,'actual-lease');assert.equal(measured.target!.generation,'owned-generation');await measured.verify();
 await fs.writeFile(path.join(r.renderer.buildRoot,'assets/unlisted.png'),'foreign');await assert.rejects(measured.verify());await fs.unlink(path.join(r.renderer.buildRoot,'assets/unlisted.png'));
 await fs.unlink(path.join(r.renderer.buildRoot,'assets/actual.woff2'));await assert.rejects(measured.verify());await fs.writeFile(path.join(r.renderer.buildRoot,'assets/actual.woff2'),Buffer.from([0,255,1,2,3]));
 await fs.unlink(path.join(r.renderer.buildRoot,'assets/actual.svg'));await fs.symlink(chunk,path.join(r.renderer.buildRoot,'assets/actual.svg'));await assert.rejects(measured.verify());
 assert.equal((await fs.stat(filename)).mode&0o777,0o600);await assert.rejects(writeRendererBuildReceipt(input));assert.deepEqual(await fs.readFile(filename),before);
 }finally{await r.cleanup();}
});
for(const mismatch of ['source-missing','package-symlink','foreign-output','no-javascript','invalid-source-hash'])test(`post-build writer rejects ${mismatch} without creating a receipt`,async()=>{
 const r=await setup();try{
 await fs.unlink(path.join(r.loom.build.root,'renderer-build.json'));
 const input={...r.renderer,receiptRoot:r.loom.build.root,sourceManifestSha256:r.loom.revision.sourceManifestSha256};
 if(mismatch==='source-missing')await fs.unlink(path.join(r.renderer.sourceRoot,'package-lock.json'));
 if(mismatch==='package-symlink'){const name=path.join(r.renderer.installedRoot,'react/package.json');await fs.unlink(name);await fs.symlink(path.join(r.renderer.installedRoot,'react-dom/package.json'),name);}
 if(mismatch==='foreign-output')input.buildRoot=r.renderer.sourceRoot;
 if(mismatch==='no-javascript')await fs.unlink(path.join(r.renderer.buildRoot,'app.js'));
 if(mismatch==='invalid-source-hash')input.sourceManifestSha256='invented';
 await assert.rejects(writeRendererBuildReceipt(input));await assert.rejects(fs.stat(path.join(r.loom.build.root,'renderer-build.json')));
 }finally{await r.cleanup();}
});
