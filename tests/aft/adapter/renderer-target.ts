import { readFile, lstat, readdir, realpath } from 'node:fs/promises';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import path from 'node:path';
import { RENDERER_SOURCE_FILES as sourceFiles, RENDERER_PACKAGE_FILES as packageFiles, RendererBuildReceipt, RendererTargetIdentity } from './renderer-contract.js';
export { RendererTargetIdentity } from './renderer-contract.js';
import { requireFact, sha256 } from './protocol.js';
import { getFixture } from './ownership.js';
import { containedPath, observeFilesystem } from './filesystem.js';
import { measureProjection, type MeasuredProjection } from './projection.js';

export interface OwnedRendererTarget {
  targetId: string; generation: string; sourceRootId: string; installedRootId: string; buildRootId: string;
  receipt: { rootId: string; relativePath: string; sha256: string };
}
/** Production composition accepts only roots registered to the fixture and a
 * source-built receipt linked to its verified frontend target/generation.
 * Test devDependencies cannot acquire that relationship through version equality. */
export async function measureFixtureProjection(context: CapabilityContext, leaseId: string): Promise<MeasuredProjection> {
  const fixture = await getFixture(context, leaseId); const target = fixture.rendererTarget;
  requireFact(target, 'unsupported-capability', 'Fixture has no attested renderer target');
  const pinnedTarget = JSON.stringify(target);
  const roots = [target.sourceRootId, target.installedRootId, target.buildRootId, target.receipt.rootId];
  const identities = roots.map(id => fixture.roots.get(id));
  requireFact(identities.every(root => root && !root.remoteObserve), 'source-mismatch', 'Renderer roots are not registered local build mirrors');
  const verifyRoots = async () => {
    await fixture.verify(context.signal); context.signal.throwIfAborted();
    requireFact(fixture.rendererTarget === target && JSON.stringify(target) === pinnedTarget, 'source-mismatch', 'Renderer target binding changed');
    for (const [i, id] of roots.entries()) {
      const root = fixture.roots.get(id); const initial = identities[i]!;
      requireFact(root === initial, 'source-mismatch', 'Renderer root registration changed');
      const stat = await lstat(initial.path);
      requireFact(!stat.isSymbolicLink() && stat.isDirectory() && stat.dev === initial.device && stat.ino === initial.inode,
        'source-mismatch', 'Renderer physical root changed');
    }
  };
  const verifyReceipt = async () => {
    await verifyRoots();
    const receiptRoot = identities[3]!;
    const result = await observeFilesystem({ leaseId, rootId: target.receipt.rootId, relativePaths: [target.receipt.relativePath],
      view: 'bytes', maxBytes: 4 * 1024 * 1024, maxEntries: 1 }, receiptRoot.path);
    const file = result.entries[0]!;
    requireFact(file.exists && file.sha256 === target.receipt.sha256 && file.contentBase64 !== null,
      'source-mismatch', 'Renderer build receipt differs from the attested target');
    const receipt = RendererBuildReceipt.parse(JSON.parse(Buffer.from(file.contentBase64, 'base64').toString('utf8')));
    requireFact(receipt.fixtureLeaseId === leaseId && receipt.targetId === target.targetId && receipt.generation === target.generation &&
      receipt.sourceRootId === target.sourceRootId && receipt.installedRootId === target.installedRootId && receipt.buildRootId === target.buildRootId,
      'source-mismatch', 'Renderer build receipt belongs to another fixture or runtime');
    for (const [index, expected] of [receipt.roots.source, receipt.roots.installed, receipt.roots.build].entries()) {
      const root = identities[index]!;
      requireFact(root.path === expected.path && root.device === expected.device && root.inode === expected.inode,
        'source-mismatch', 'Renderer receipt roots differ from the runtime-bound build mirrors');
    }
    requireFact(new Set(receipt.sources.map(file => file.relativePath)).size === 6 && sourceFiles.every(name => receipt.sources.some(file => file.relativePath === name)) &&
      new Set(receipt.packages.map(file => file.relativePath)).size === 7 && packageFiles.every(name => receipt.packages.some(file => file.relativePath === name)),
      'source-mismatch', 'Renderer build receipt omits the reviewed source/dependency closure');
    requireFact(new Set(receipt.build.map(file => file.relativePath)).size === receipt.build.length && receipt.build.some(file => file.relativePath.endsWith('.js')),
      'source-mismatch', 'Renderer build output closure is invalid');
    for (const [index, entries] of [receipt.sources, receipt.packages, receipt.build].entries()) {
      const root = identities[index]!;
      for (const expected of entries) {
        const actual = await observeFilesystem({ leaseId, rootId: roots[index]!, relativePaths: [expected.relativePath], view: 'tree-digest',
          maxBytes: 16 * 1024 * 1024, maxEntries: 1 }, root.path);
        requireFact(actual.entries[0]?.kind === 'file' && actual.entries[0].sha256 === expected.sha256,
          'source-mismatch', 'Renderer source, installed package or build bytes changed');
      }
    }
    const closure=await rendererBuildClosure(identities[2]!.path);
    requireFact(JSON.stringify(closure)===JSON.stringify([...receipt.build].sort((a,b)=>a.relativePath<b.relativePath?-1:a.relativePath>b.relativePath?1:0)),
      'source-mismatch','Renderer build receipt omits or changes output files');
    await verifyRoots();
    return receipt;
  };
  await verifyReceipt();
  const measured = await measureProjection(identities[0]!.path, identities[1]!.path);
  const identity = RendererTargetIdentity.parse({ targetId: target.targetId, generation: target.generation, receiptSha256: target.receipt.sha256,
    sourceRootId: target.sourceRootId, installedRootId: target.installedRootId, buildRootId: target.buildRootId });
  return { identity: measured.identity, target: identity, fixtureLeaseId: leaseId,
    async verify() { await getFixture(context, leaseId); await verifyReceipt(); return measured.verify(); } };
}
/** Receipt construction belongs to the attested build pipeline, not suite data.
 * Reads the actual reviewed files and built outputs; it does not copy hashes. */
export async function rendererBuildFileReceipts(root: string, relativePaths: readonly string[]) {
  const receipts: { relativePath: string; sha256: string }[] = [];
  for (const relativePath of relativePaths) receipts.push({ relativePath, sha256: await sha256(await readFile(await containedPath(root, relativePath))) });
  return receipts;
}

/** Complete regular-file output closure, including assets with non-text suffixes. */
export async function rendererBuildClosure(root:string) {
  const before=await lstat(root);
  requireFact(before.isDirectory()&&!before.isSymbolicLink()&&await realpath(root)===root,'source-mismatch','Renderer output root is not canonical');
  const files:string[]=[];
  const walk=async(relative:string,depth:number)=>{
    requireFact(depth<=100,'source-mismatch','Renderer output depth exceeds bound');
    for(const entry of await readdir(path.join(root,relative),{withFileTypes:true})) {
      requireFact(!entry.isSymbolicLink(),'source-mismatch','Renderer output closure contains a symlink');
      const name=relative?`${relative}/${entry.name}`:entry.name;
      if(entry.isDirectory())await walk(name,depth+1);
      else {requireFact(entry.isFile(),'source-mismatch','Renderer output entry is not a regular file');files.push(name);}
      requireFact(files.length<=50000,'source-mismatch','Renderer output closure exceeds bound');
    }
  };
  await walk('',0);const entries=[];
  for(const relativePath of files.sort()) {
    const observed=await observeFilesystem({leaseId:'renderer-build',rootId:'output',relativePaths:[relativePath],view:'tree-digest',maxBytes:16*1024*1024,maxEntries:1},root);
    requireFact(observed.entries[0]?.kind==='file'&&observed.entries[0].sha256,'source-mismatch','Renderer output file is unreadable');
    entries.push({relativePath,sha256:observed.entries[0].sha256});
  }
  const after=await lstat(root);
  requireFact(before.dev===after.dev&&before.ino===after.ino&&!after.isSymbolicLink(),'source-mismatch','Renderer output root changed');
  return entries;
}
