import { lstat, realpath, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import { sha256, Digest, RelativePath } from '../protocol.js';
import { rendererBuildClosure, type OwnedRendererTarget } from '../renderer-target.js';
import { RENDERER_SOURCE_FILES, RENDERER_PACKAGE_FILES, RendererFileSchema as File, RendererSourceBuildReceipt as Build, RendererBuildReceipt } from '../renderer-contract.js';
import { observeFilesystem } from '../filesystem.js';
import type { OwnedFixture } from '../ownership.js';
import type { RegisteredBuild } from './production.js';
import { FixtureError } from './lifecycle.js';

const check = (value:unknown) => { if (!value) throw new FixtureError('source-mismatch'); };
export const rendererSources = RENDERER_SOURCE_FILES;
export const rendererPackages = RENDERER_PACKAGE_FILES;
export interface RendererBuildCoordinates {
  sourceRoot:string;installedRoot:string;buildRoot:string;receiptRoot:string;sourceManifestSha256:string;
}
/** Called by the trusted build launcher after compilation and before sealing
 * the enclosing build manifest. Reads existing bytes; never builds or repairs. */
export async function writeRendererBuildReceipt(input:RendererBuildCoordinates) {
  Digest.parse(input.sourceManifestSha256);
  const roots=[input.sourceRoot,input.installedRoot,input.buildRoot,input.receiptRoot];
  const stamps=await Promise.all(roots.map(async root=>{
    check(path.isAbsolute(root)&&path.normalize(root)===root);
    const stat=await lstat(root);check(!stat.isSymbolicLink()&&stat.isDirectory()&&await realpath(root)===root);return stat;
  }));
  const relative=(root:string)=>{const value=path.relative(input.receiptRoot,root).split(path.sep).join('/');RelativePath.parse(value);check(path.join(input.receiptRoot,value)===root);return value;};
  const installedRelativeRoot=relative(input.installedRoot),buildRelativeRoot=relative(input.buildRoot);
  check(input.installedRoot!==input.buildRoot&&!input.installedRoot.startsWith(input.buildRoot+path.sep)&&!input.buildRoot.startsWith(input.installedRoot+path.sep));
  const output=await rendererBuildClosure(input.buildRoot);check(output.some(file=>file.relativePath.endsWith('.js')));
  let consumed=0;
  const read=async(root:string,files:readonly string[])=>{const entries:z.infer<typeof File>[]=[];for(const relativePath of [...files].sort()){
    const actual=await observeFilesystem({leaseId:'renderer-build',rootId:'closure',relativePaths:[relativePath],view:'tree-digest',maxBytes:16*1024*1024,maxEntries:1},root);
    const entry=actual.entries[0];check(entry?.kind==='file'&&entry.sha256);consumed+=entry!.bytes!;check(consumed<=256*1024*1024);
    entries.push(File.parse({relativePath,sha256:entry!.sha256}));
  }return entries;};
  const receipt=Build.parse({version:1,sourceManifestSha256:input.sourceManifestSha256,installedRelativeRoot,buildRelativeRoot,
    sources:await read(input.sourceRoot,rendererSources),packages:await read(input.installedRoot,rendererPackages),build:output});
  check(JSON.stringify(await rendererBuildClosure(input.buildRoot))===JSON.stringify(output));
  for(const [index,root] of roots.entries()){
    const after=await lstat(root),before=stamps[index]!;check(before.dev===after.dev&&before.ino===after.ino&&await realpath(root)===root);
  }
  const bytes=JSON.stringify(receipt);check(Buffer.byteLength(bytes)<=4*1024*1024);
  const relativePath='renderer-build.json';await writeFile(path.join(input.receiptRoot,relativePath),bytes,{flag:'wx',mode:0o600});
  return {relativePath,sha256:await sha256(bytes),receipt};
}
export interface PreparedRenderer {
  sourceRoot:string;installedRoot:string;buildRoot:string;
  sources:z.infer<typeof File>[];packages:z.infer<typeof File>[];build:z.infer<typeof File>[];
}
/** renderer-build.json is an attested output of the reviewed source build, not
 * a suite input or a development mirror. Every closure byte must be in its
 * enclosing source/build manifest and match its actual file. */
export async function prepareRenderer(loom:RegisteredBuild):Promise<PreparedRenderer> {
  const receiptEntry=loom.build.entries.find(entry=>entry.relativePath==='renderer-build.json');check(receiptEntry);
  const filename=path.join(loom.build.root,'renderer-build.json');
  const stat=await lstat(filename);check(stat.isFile()&&!stat.isSymbolicLink()&&stat.size<=4*1024*1024&&await realpath(filename)===filename);
  const observed=await observeFilesystem({leaseId:'renderer-preflight',rootId:'build',relativePaths:['renderer-build.json'],view:'bytes',maxBytes:4*1024*1024,maxEntries:1},loom.build.root);
  check(observed.entries[0]?.sha256===receiptEntry!.sha256&&observed.entries[0].contentBase64);
  const bytes=Buffer.from(observed.entries[0]!.contentBase64!,'base64');
  const receipt=Build.parse(JSON.parse(bytes.toString('utf8')));check(receipt.sourceManifestSha256===loom.revision.sourceManifestSha256);
  const prepared={sourceRoot:path.join(loom.source.root,'internal/webui/frontend'),installedRoot:path.join(loom.build.root,receipt.installedRelativeRoot),
    buildRoot:path.join(loom.build.root,receipt.buildRelativeRoot),sources:receipt.sources,packages:receipt.packages,build:receipt.build};
  check(new Set(receipt.sources.map(f=>f.relativePath)).size===6&&rendererSources.every(p=>receipt.sources.some(f=>f.relativePath===p)));
  check(new Set(receipt.packages.map(f=>f.relativePath)).size===7&&rendererPackages.every(p=>receipt.packages.some(f=>f.relativePath===p)));
  check(new Set(receipt.build.map(f=>f.relativePath)).size===receipt.build.length&&receipt.build.some(f=>f.relativePath.endsWith('.js')));
  for(const [root,entries,manifest,prefix] of [
    [prepared.sourceRoot,receipt.sources,loom.source,'internal/webui/frontend'],
    [prepared.installedRoot,receipt.packages,loom.build,receipt.installedRelativeRoot],
    [prepared.buildRoot,receipt.build,loom.build,receipt.buildRelativeRoot],
  ] as const){
    const before=await lstat(root);check(await realpath(root)===root&&before.isDirectory());
    for(const entry of entries){
      check(manifest.entries.some(f=>f.relativePath===`${prefix}/${entry.relativePath}`&&f.sha256===entry.sha256));
      const actual=await observeFilesystem({leaseId:'renderer-preflight',rootId:'closure',relativePaths:[entry.relativePath],view:'tree-digest',maxBytes:16*1024*1024,maxEntries:1},root);
      check(actual.entries[0]?.kind==='file'&&actual.entries[0].sha256===entry.sha256);
    }
    const after=await lstat(root);check(before.dev===after.dev&&before.ino===after.ino&&await realpath(root)===root);
  }
  check(JSON.stringify(await rendererBuildClosure(prepared.buildRoot))===JSON.stringify([...receipt.build].sort((a,b)=>a.relativePath<b.relativePath?-1:a.relativePath>b.relativePath?1:0)));
  return prepared;
}
export async function bindRenderer(loom:RegisteredBuild,leaseId:string,target:{targetId:string;generation:string;buildRoot:string},receiptRoot:string,
  roots:OwnedFixture['roots']):Promise<OwnedRendererTarget> {
  const renderer=await prepareRenderer(loom);check(target.targetId&&target.generation&&target.buildRoot===renderer.buildRoot);
  const physical=async(root:string)=>{const stat=await lstat(root);check(!stat.isSymbolicLink()&&stat.isDirectory()&&await realpath(root)===root);return {path:root,device:stat.dev,inode:stat.ino};};
  const source=await physical(renderer.sourceRoot),installed=await physical(renderer.installedRoot),build=await physical(renderer.buildRoot);
  const receiptIdentity=await physical(receiptRoot);
  const ids={sourceRootId:'renderer-source',installedRootId:'renderer-installed',buildRootId:'renderer-build'};
  const bytes=JSON.stringify(RendererBuildReceipt.parse({version:1,fixtureLeaseId:leaseId,targetId:target.targetId,generation:target.generation,...ids,roots:{source,installed,build},
    sources:renderer.sources,packages:renderer.packages,build:renderer.build}));
  const relativePath='renderer-target.json';await writeFile(path.join(receiptRoot,relativePath),bytes,{flag:'wx',mode:0o600});
  roots.set(ids.sourceRootId,source);roots.set(ids.installedRootId,installed);roots.set(ids.buildRootId,build);roots.set('renderer-receipt',receiptIdentity);
  return {targetId:target.targetId,generation:target.generation,...ids,receipt:{rootId:'renderer-receipt',relativePath,sha256:await sha256(bytes)}};
}
