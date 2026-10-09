import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';
import { mkdtemp, realpath, rm, mkdir, readFile, writeFile } from 'node:fs/promises';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { createProjectionProvider, measureProjection, pinProjectionImplementation, MarkdownOutputSchema } from './projection.js';

const root = fileURLToPath(new URL('.', import.meta.url));
const frontend = path.resolve(root, '../../../internal/webui/frontend');
const installed = path.join(root, 'node_modules');
async function harness(t: { after(fn: () => Promise<void>): void }) {
  const pin = await pinProjectionImplementation(root);
  assert.ok(pin.files.some(file => file.path.startsWith('projections/node_modules/remark-parse/')));
  assert.ok(pin.files.some(file => file.path.startsWith('projections/node_modules/micromark/')));
  const measured = await measureProjection(frontend, installed);
  const registry = new CapabilityRegistry().register(createProjectionProvider(pin, measured));
  const context = createCapabilityContext({ file: 'markdown-interface.yaml', line: 1 }, registry);
  const evidence = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-projection-evidence-')));
  t.after(() => rm(evidence, { recursive: true }));
  putEvidenceStore(context, await createEvidenceStore(evidence));
  return { registry, context, measured, invoke: (input: unknown) => registry.invoke({ id: 'loom.markdown.project', version: 1, input: {} }, input, context) };
}
test('public Markdown capability measures source and installed identity and returns independent golden projection', async t => {
  const h = await harness(t);
  const result = await h.invoke({ mode: 'motion', answer: 'Hello **world**', frames: [{ text: 'Hello world', streaming: false }], arrivals: [] });
  assert.equal(result.availability, 'observed'); assert.equal(result.provenance.evidenceClass, 'deterministic');
  const output = MarkdownOutputSchema.parse(result.data);
  assert.ok(output.mode === 'motion'); assert.equal(output.terminal, 'Hello world'); assert.equal(output.frames[0]!.content, 'Hello world');
  assert.equal(output.sourceUtf16, 15);
  assert.ok(result.provenance.artifacts[0]!.sha256);
  await assert.rejects(h.invoke({ mode: 'motion', answer: 'text', frames: [], arrivals: [], sourceRoot: '/foreign' }));
  const unknown = await h.invoke({ mode: 'motion', answer: 'Hello', frames: [{ text: 'foreign frame', streaming: true }], arrivals: [] });
  assert.equal(unknown.availability, 'incomplete'); assert.equal(unknown.data, undefined);
  const terminal = await h.invoke({ mode: 'terminal-full', answer: 'large '.repeat(3000), frames: [], arrivals: [] });
  assert.equal(terminal.availability, 'observed'); assert.ok(MarkdownOutputSchema.parse(terminal.data).sourceUtf16 > 17000);
});
test('measured projection refuses copied constants when actual source or installed package differs', async t => {
  const temporary = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-projection-source-')));
  t.after(() => rm(temporary, { recursive: true }));
  const relative = 'src/components/AgentChat/ChatMarkdown.tsx';
  await mkdir(path.dirname(path.join(temporary, relative)), { recursive: true });
  await writeFile(path.join(temporary, relative), await readFile(path.join(frontend, relative)));
  await assert.rejects(measureProjection(temporary, installed));
  const packages = path.join(temporary, 'packages'); await mkdir(path.join(packages, 'react'), { recursive: true });
  await writeFile(path.join(packages, 'react/package.json'), JSON.stringify({ name: 'react', version: 'foreign' }));
  await assert.rejects(measureProjection(frontend, packages));
});
