import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { readdir, readFile, mkdtemp, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin } from '@tysonthomas9/aft/capabilities';
import { pinProjectionImplementation } from './projection.js';
import type * as Adapter from './index.js';

const root = fileURLToPath(new URL('.', import.meta.url));
test('emitted source-built adapter registers actual providers and retains fixture catalog/parser bytes', async t => {
  const emitted: typeof Adapter = await import(pathToFileURL(path.join(root, 'dist/index.js')).href);
  assert.deepEqual(await readFile(path.join(root, 'dist/legacy/scenarios.json')), await readFile(path.join(root, 'legacy/scenarios.json')));
  const receipt = JSON.parse(await readFile(path.join(root, 'dist/build-receipt.json'), 'utf8')) as { runtimeFiles: { path: string; sha256: string }[] };
  assert.ok(receipt.runtimeFiles.some(file => file.path === 'dist/legacy/scenarios.json'));
  assert.ok(receipt.runtimeFiles.some(file => file.path.startsWith('dist/projections/node_modules/micromark/')));
  const files = (await readdir(root)).filter(name => name.endsWith('.ts') && !name.endsWith('.test.ts'));
  const corePin = calculateImplementationPin(root, [...files, ...receipt.runtimeFiles.map(file => file.path), 'dist/build-receipt.json'], 'dist/index.js', 'createCoreProviders');
  const registry = new CapabilityRegistry();
  for (const provider of emitted.createCoreProviders(corePin)) registry.register(provider);
  const measured = await emitted.measureProjection(path.resolve(root, '../../../internal/webui/frontend'), path.join(root, 'node_modules'));
  const projectionPin = await pinProjectionImplementation(root, 'emitted');
  assert.ok(projectionPin.files.some(file => file.path.startsWith('dist/projections/node_modules/micromark/')));
  registry.register(emitted.createProjectionProvider(projectionPin, measured));
  const context = createCapabilityContext({ file: 'emitted-contract.yaml', line: 1 }, registry);
  const evidence = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-emitted-adapter-')));
  t.after(() => rm(evidence, { recursive: true }));
  emitted.putEvidenceStore(context, await emitted.createEvidenceStore(evidence));
  const result = await registry.invoke({ id: 'loom.markdown.project', version: 1, input: {} },
    { mode: 'motion', answer: 'actual **compiled** bytes', frames: [{ text: 'actual compiled bytes', streaming: false }], arrivals: [] }, context);
  assert.equal(result.availability, 'observed');
  const actual = emitted.MarkdownOutputSchema.parse(result.data);
  assert.equal(actual.terminal, 'actual compiled bytes');
});

test('renderer source and emitted modules initialize independently without import-order authority',async()=>{
  const run=promisify(execFile);
  for(const module of ['renderer-contract','renderer-target','fixture/host']) {
    await run(process.execPath,['--input-type=module','--eval',`await import('./dist/${module}.js')`],{cwd:root});
    await run(path.join(root,'node_modules/.bin/tsx'),['--eval',`import('./${module}.ts').catch(error=>{console.error(error);process.exitCode=1})`],{cwd:root});
  }
});
