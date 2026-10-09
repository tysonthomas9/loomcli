import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { mkdtemp, mkdir, copyFile, lstat, realpath, rm, writeFile } from 'node:fs/promises';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createProjectionProvider, pinProjectionImplementation } from './projection.js';
import { rendererBuildFileReceipts, measureFixtureProjection } from './renderer-target.js';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { putFixture, type OwnedFixture, type OwnedRoot } from './ownership.js';
import { sha256 } from './protocol.js';

const root = fileURLToPath(new URL('.', import.meta.url));
const frontend = path.resolve(root, '../../../internal/webui/frontend');
test('owned renderer composition rejects matching versions in an unrelated root and foreign target receipts', async t => {
  const owned = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-renderer-binding-')));
  t.after(() => rm(owned, { recursive: true }));
  const source = path.join(owned, 'source'); const installed = path.join(owned, 'packages'); const build = path.join(owned, 'build');
  await mkdir(source); await mkdir(installed); await mkdir(build); await mkdir(path.join(build, 'assets'));
  // This is a deterministic mirror/receipt fixture, not runtime renderer proof.
  const sourceNames = ['src/components/AgentChat/ChatMarkdown.tsx', 'src/components/AgentChat/LongText.tsx',
    'src/components/AgentChat/codeHighlight.ts', 'src/components/AgentChat/MessageCopyButton.tsx', 'src/components/AgentChat/ChatMarkdown.module.css', 'package-lock.json'];
  for (const relative of sourceNames) { const dest = path.join(source, relative); await mkdir(path.dirname(dest), { recursive: true }); await copyFile(path.join(frontend, relative), dest); }
  const packageNames = ['react', 'react-dom', 'react-markdown', 'remark-gfm', 'rehype-sanitize', 'esbuild', 'jsdom'].map(name => `${name}/package.json`);
  for (const relative of packageNames) { const dest = path.join(installed, relative); await mkdir(path.dirname(dest), { recursive: true }); await copyFile(path.join(root, 'node_modules', relative), dest); }
  await writeFile(path.join(build, 'assets/main.js'), '/* deterministic receipt fixture */');
  const stamp = async (directory: string): Promise<OwnedRoot> => { const stat = await lstat(directory); return { path: directory, device: stat.dev, inode: stat.ino }; };
  const roots = new Map([['source', await stamp(source)], ['installed', await stamp(installed)], ['build', await stamp(build)], ['receipt', await stamp(owned)]]);
  const registry = new CapabilityRegistry(); registry.register(createProjectionProvider(await pinProjectionImplementation(root)));
  const context = createCapabilityContext({ file: 'owned-renderer-contract.yaml', line: 1 }, registry);
  putEvidenceStore(context, await createEvidenceStore(owned));
  const receipt = { version: 1, fixtureLeaseId: 'lease', targetId: 'frontend-target', generation: 'frontend-generation',
    sourceRootId: 'source', installedRootId: 'installed', buildRootId: 'build',
    roots: { source: roots.get('source'), installed: roots.get('installed'), build: roots.get('build') },
    sources: await rendererBuildFileReceipts(source, sourceNames), packages: await rendererBuildFileReceipts(installed, packageNames),
    build: await rendererBuildFileReceipts(build, ['assets/main.js']) };
  const serialized = JSON.stringify(receipt); await writeFile(path.join(owned, 'receipt.json'), serialized);
  const unavailable = async (): Promise<never> => { throw new Error('No product transport used'); };
  const fixture: OwnedFixture = { leaseId: 'lease', runId: context.runId, suiteId: context.suiteId, scope: context.scope, caseId: context.caseId,
    workspaceId: 'workspace', repo: source, profile: 'deterministic-receipt-contract', expiresAtUtcMs: Number.MAX_SAFE_INTEGER,
    evidenceClass: 'deterministic', roots, agents: new Map(), secrets: [], readApi: unavailable, readFiles: unavailable, resolveAgent: unavailable,
    verify: async () => {}, dispose: async () => {}, rendererTarget: { targetId: receipt.targetId, generation: receipt.generation,
      sourceRootId: 'source', installedRootId: 'installed', buildRootId: 'build', receipt: { rootId: 'receipt', relativePath: 'receipt.json', sha256: await sha256(serialized) } } };
  putFixture(context, fixture);
  const actual = await registry.invoke({ id: 'loom.markdown.project', version: 1, input: {} },
    { leaseId: 'lease', mode: 'motion', answer: '**owned** source', frames: [], arrivals: [] }, context);
  assert.equal(actual.availability, 'observed'); assert.equal(actual.provenance.identity.fixtureLeaseId, 'lease');
  const measured = await measureFixtureProjection(context, 'lease'); assert.equal(measured.target!.generation, receipt.generation);
  const original = roots.get('installed')!;
  roots.set('installed', await stamp(path.join(root, 'node_modules')));
  await assert.rejects(measureFixtureProjection(context, 'lease'));
  roots.set('installed', original);
  fixture.rendererTarget!.generation = 'foreign-generation'; await assert.rejects(measured.verify());
  await assert.rejects(measureFixtureProjection({ ...context, runId: 'foreign-run' }, 'lease'));
});
