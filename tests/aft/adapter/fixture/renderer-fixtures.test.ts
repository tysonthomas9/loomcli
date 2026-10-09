import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import type { RegisteredBuild } from './production.js';
import { rendererSources, rendererPackages, writeRendererBuildReceipt } from './renderer.js';
const hash=(v:string|Uint8Array)=>createHash('sha256').update(v).digest('hex');
export async function materializeRenderer(loom:RegisteredBuild,buildRelativeRoot='renderer-dist') {
 const sourceRoot=path.join(loom.source.root,'internal/webui/frontend'),installedRelativeRoot='renderer-packages';
 const put=async(root:string,relativePath:string,bytes:string,entries:RegisteredBuild['build']['entries'])=>{
  const filename=path.join(root,relativePath);await fs.mkdir(path.dirname(filename),{recursive:true});await fs.writeFile(filename,bytes);
  const entry={relativePath,sha256:hash(bytes)},index=entries.findIndex(e=>e.relativePath===relativePath);if(index<0)entries.push(entry);else entries[index]=entry;
 };
 for(const file of rendererSources)await put(loom.source.root,`internal/webui/frontend/${file}`,`deterministic source ${file}`,loom.source.entries);
 for(const file of rendererPackages)await put(loom.build.root,`${installedRelativeRoot}/${file}`,JSON.stringify({name:file.split('/')[0],version:'1.0.0'}),loom.build.entries);
 for(const file of ['index.html','app.js'])await put(loom.build.root,`${buildRelativeRoot}/${file}`,`deterministic build ${file}`,loom.build.entries);
 const digest=(entries:RegisteredBuild['build']['entries'])=>hash([...entries].sort((a,b)=>a.relativePath<b.relativePath?-1:1).map(e=>`${e.sha256}  ${e.relativePath}\n`).join(''));
 loom.revision.sourceManifestSha256=digest(loom.source.entries);
 const receipt=await writeRendererBuildReceipt({sourceRoot,installedRoot:path.join(loom.build.root,installedRelativeRoot),buildRoot:path.join(loom.build.root,buildRelativeRoot),receiptRoot:loom.build.root,sourceManifestSha256:loom.revision.sourceManifestSha256});
 loom.build.entries.push({relativePath:receipt.relativePath,sha256:receipt.sha256});loom.revision.buildManifestSha256=digest(loom.build.entries);
 return {sourceRoot,installedRoot:path.join(loom.build.root,installedRelativeRoot),buildRoot:path.join(loom.build.root,buildRelativeRoot)};
}
