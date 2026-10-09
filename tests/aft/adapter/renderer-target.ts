import { readFile, lstat } from 'node:fs/promises';
import { z } from 'zod';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { Id, Digest, RelativePath, requireFact, sha256 } from './protocol.js';
import { getFixture } from './ownership.js';
import { containedPath, observeFilesystem } from './filesystem.js';
import { measureProjection, type MeasuredProjection } from './projection.js';

const sourceFiles = ['src/components/AgentChat/ChatMarkdown.tsx', 'src/components/AgentChat/LongText.tsx',
  'src/components/AgentChat/codeHighlight.ts', 'src/components/AgentChat/MessageCopyButton.tsx',
  'src/components/AgentChat/ChatMarkdown.module.css', 'package-lock.json'];
const packageFiles = ['react', 'react-dom', 'react-markdown', 'remark-gfm', 'rehype-sanitize', 'esbuild', 'jsdom'].map(name => `${name}/package.json`);
const File = z.object({ relativePath: RelativePath, sha256: Digest }).strict();
export const RendererTargetIdentity = z.object({ targetId: Id, generation: Id, receiptSha256: Digest,
  sourceRootId: Id, installedRootId: Id, buildRootId: Id }).strict();
export interface OwnedRendererTarget {
  targetId: string; generation: string; sourceRootId: string; installedRootId: string; buildRootId: string;
  receipt: { rootId: string; relativePath: string; sha256: string };
}
const PhysicalRoot = z.object({ path: Id, device: z.number().int().nonnegative(), inode: z.number().int().nonnegative() }).strict();
const RendererBuildReceipt = z.object({ version: z.literal(1), fixtureLeaseId: Id, targetId: Id, generation: Id,
  sourceRootId: Id, installedRootId: Id, buildRootId: Id,
  roots: z.object({ source: PhysicalRoot, installed: PhysicalRoot, build: PhysicalRoot }).strict(),
  sources: z.array(File).min(6).max(6), packages: z.array(File).min(7).max(7), build: z.array(File).min(1).max(50000),
}).strict();
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
    requireFact(new Set(receipt.build.map(file => file.relativePath)).size === receipt.build.length && receipt.build.some(file => file.relativePath.endsWith('.js')) &&
      receipt.build.every(file => /\.(?:js|css|html|json|map)$/.test(file.relativePath)),
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
