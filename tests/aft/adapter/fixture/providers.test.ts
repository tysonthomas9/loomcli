import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm, readFile, readdir, lstat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { CapabilityRegistry, createCapabilityContext, getRegisteredResource, calculateImplementationPin, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createEvidenceStore, putEvidenceStore } from '../evidence.js';
import { getFixture, fixturesKey, disposeFixtures } from '../ownership.js';
import { createFixtureProviders, type FixtureProviderOptions } from './providers.js';
import { type FixtureDriver, type FixturePlan, type Resource } from './lifecycle.js';

async function setup(t: { after(fn: () => Promise<void>): void }) {
  const fixtureRoot = fileURLToPath(new URL('.', import.meta.url)); const root = path.dirname(fixtureRoot);
  const evidenceRoot = await mkdtemp(path.join(fixtureRoot, 'test-artifacts-'));
  t.after(() => rm(evidenceRoot, { recursive: true }));
  const store = await createEvidenceStore(evidenceRoot);
  const files = [...(await readdir(root)).filter(file => file.endsWith('.ts') && !file.endsWith('.test.ts')),
    ...(await readdir(fixtureRoot)).filter(file => file.endsWith('.ts') && !file.endsWith('.test.ts')).map(file => `fixture/${file}`)];
  const pin = calculateImplementationPin(root, files, 'fixture/providers.ts', 'createFixtureProviders');
  const revision = { repository: 'loom', commit: 'a'.repeat(40), tree: 'b'.repeat(40), sourceManifestSha256: 'c'.repeat(64), buildManifestSha256: 'd'.repeat(64) };
  const plan: FixturePlan = { profile: 'agents-real-opencode', loomRevision: revision, fleetRevision: revision, engineRevision: revision, adapterRevision: revision,
    model: 'openai/model', caseCount: 1, maxCases: 10, selectionSha256: 'e'.repeat(64), leaseDurationMs: 10000 };
  let driverCalls = 0; let provisionFails = false; let cleanupFails = false; let incomplete = false; let changed = false; let bindFails = false;
  const calls: string[] = [];
  const driver: FixtureDriver = {
    async preflight() { calls.push('preflight'); }, async identity() { return !changed; },
    async allocate(_id, _run, record) { record({ id: evidenceRoot, kind: 'directory', generation: 'owned-dir' }); },
    async provision(_plan, record) {
      record({ id: 'compose', kind: 'compose', generation: 'owned-project' }); calls.push('provision');
      if (provisionFails) throw new Error('Bearer secret-provision-token');
      return { apiOrigin: 'http://127.0.0.1:4100', filesOrigin: 'http://127.0.0.1:4101', workspaceId: 'workspace', repo: '/owned/source' };
    },
    async inspect(resource) { return { owned: true, complete: !incomplete, services: resource.kind === 'compose' ? [{ id: 'service', pid: 40, generation: 'owned-generation', state: 'running' }] : [] }; },
    async remove(resource: Resource) { calls.push(`remove:${resource.id}`); if (cleanupFails) throw new Error('password=secret-cleanup-token'); },
    async artifact(kind, value) { return { ...await store.retain(JSON.stringify({ kind, value })), redaction: 'sanitized' as const }; },
  };
  const options: FixtureProviderOptions = { implementation: pin, implementationSha256: pin.sha256, plans: [plan],
    driver() { driverCalls++; return driver; }, evidenceAfterFailure: async () => store,
    async bind() { if (bindFails) throw new Error("Bearer private-bind-token"); const stat = await lstat(evidenceRoot); return { evidenceClass: 'deterministic', evidenceStore: store,
      roots: new Map([['runtime', { path: evidenceRoot, device: stat.dev, inode: stat.ino }]]), secrets: [],
      readApi: async () => ({ status: 200, body: {} }), readFiles: async () => ({ status: 200, body: {} }),
      resolveAgent: async () => { throw new Error('No test agent'); } }; },
  };
  const registry = new CapabilityRegistry(); for (const provider of createFixtureProviders(options)) registry.register(provider);
  const context = createCapabilityContext({ file: 'fixture.test.yaml', line: 1 }, registry, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002');
  putEvidenceStore(context, store);
  const input = { runId: context.runId, profile: plan.profile, loomRevision: revision, fleetRevision: revision, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 };
  const invoke = (id: string, data: unknown, ctx: CapabilityContext = context) => registry.invoke({ id, version: 1, input: {} }, data, ctx);
  return { registry, context, input, invoke, store, options, calls, get driverCalls() { return driverCalls; },
    failBind() { bindFails = true; }, failProvision() { provisionFails = true; }, failCleanup(value: boolean) { cleanupFails = value; }, incomplete() { incomplete = true; }, changeSource() { changed = true; } };
}
function leaseId(result: Awaited<ReturnType<CapabilityRegistry['invoke']>>) {
  assert.equal(result.availability, 'observed'); return (result.data as { lease: { id: string } }).lease.id;
}
test('fixture operations use the canonical store and retained output receipt without owner tokens', async t => {
  const r = await setup(t); const result = await r.invoke('loom.fixture.acquire', r.input); const id = leaseId(result);
  const fixture = await getFixture(r.context, id);
  assert.equal(fixture.leaseId, id); assert.equal(r.context.resources.get(`${fixturesKey}:${id}`), fixture);
  assert.equal(JSON.stringify(result).includes('ownerToken'), false);
  assert.equal((result.data as { syntheticProbeHandle: string }).syntheticProbeHandle, fixture.syntheticProbe?.handle);
  assert.equal(JSON.stringify(result).includes(fixture.syntheticProbe!.value), false);
  const retained = await r.store.resolve(result.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), result.data);
  const observed = await r.invoke('loom.fixture.observe', { leaseId: id }); assert.equal(observed.availability, 'observed');
  const release = await r.invoke('loom.fixture.release', { leaseId: id }); assert.equal(release.availability, 'observed');
  assert.equal((release.data as { released: boolean }).released, true); assert.equal(r.context.resources.has(`${fixturesKey}:${id}`), false);
});
test('unknown executable/code/module parameters fail before acquiring a driver', async t => {
  const r = await setup(t);
  for (const field of ['command', 'modulePath', 'code', 'leaseId', 'browser']) await assert.rejects(r.invoke('loom.fixture.acquire', { ...r.input, [field]: 'unsafe' }));
  assert.equal(r.driverCalls, 0);
});
test('wrong run, source, model and selection identity never allocate a driver', async t => {
  const r = await setup(t);
  for (const changed of [{ runId: 'foreign' }, { model: 'aft/m' }, { selectionSha256: 'f'.repeat(64) }, { loomRevision: { ...r.input.loomRevision, commit: 'f'.repeat(40) } }]) {
    const result = await r.invoke('loom.fixture.acquire', { ...r.input, ...changed }); assert.equal(result.availability, 'error'); assert.equal(result.data, undefined);
  }
  assert.equal(r.driverCalls, 0);
});
test('suite fixture is shared only through declared handles and cannot be released by a child case', async t => {
  const r = await setup(t); const suite = r.context; suite.scope = 'suite';
  const id = leaseId(await r.invoke('loom.fixture.acquire', r.input, suite));
  const child = (handles: string[]) => {
    const context = createCapabilityContext(r.context.source, r.registry, r.context.runId as `${string}-${string}-${string}-${string}-${string}`);
    context.suiteId = suite.suiteId;
    context.suite = { id: suite.suiteId, handles, getResource(key, handle) {
      assert.ok(handles.includes(handle)); return getRegisteredResource(suite, key, handle);
    } }; return context;
  };
  const one = child([id]); assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id }, one)).availability, 'observed');
  assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id }, child([]))).availability, 'error');
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: id }, one)).availability, 'error');
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: id }, suite)).availability, 'observed');
});
test('expired fixture cannot observe but owner can release it; abort-safe final disposal remains available', async t => {
  const r = await setup(t); const id = leaseId(await r.invoke('loom.fixture.acquire', r.input));
  const fixture = await getFixture(r.context, id); fixture.expiresAtUtcMs = 0;
  assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id })).availability, 'error');
  const controller = new AbortController(); controller.abort(); r.context.signal = controller.signal;
  await disposeFixtures(r.context); assert.equal(r.context.resources.has(`${fixturesKey}:${id}`), false);
});
test('partial acquire with cleanup failure is registered for canonical final disposal, never observed as ready', async t => {
  const r = await setup(t); r.failProvision(); r.failCleanup(true);
  const result = await r.invoke('loom.fixture.acquire', r.input); assert.equal(result.availability, 'error'); assert.equal(result.data, undefined);
  const fixtureKey = [...r.context.resources.keys()].find(key => key.startsWith(`${fixturesKey}:`)); assert.ok(fixtureKey);
  const id = fixtureKey.slice(fixturesKey.length + 1);
  assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id })).availability, 'error');
  r.failCleanup(false); await disposeFixtures(r.context); assert.equal(r.context.resources.has(fixtureKey), false);
  assert.equal(JSON.stringify(result).includes('secret-provision-token'), false);
});
test('teardown failure returns a safe released=false receipt and leaves ownership registered for retry', async t => {
  const r = await setup(t); const id = leaseId(await r.invoke('loom.fixture.acquire', r.input)); r.failCleanup(true);
  const failed = await r.invoke('loom.fixture.release', { leaseId: id }); assert.equal(failed.availability, 'observed');
  const data = failed.data as { released: boolean; remainingOwnedResources: string[]; receipt: { id: string } };
  assert.equal(data.released, false); assert.ok(data.remainingOwnedResources.length); assert.ok(r.context.resources.has(`${fixturesKey}:${id}`));
  assert.ok(await r.store.resolve(data.receipt.id)); assert.equal(JSON.stringify(failed).includes('secret-cleanup-token'), false);
  r.failCleanup(false); await disposeFixtures(r.context);
});
test('unreadable/incomplete or changed source observations cannot return empty successful inventories', async t => {
  const r = await setup(t); const id = leaseId(await r.invoke('loom.fixture.acquire', r.input)); r.incomplete();
  assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id })).availability, 'error');
  r.changeSource(); assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id })).availability, 'error');
});

test('failed transport binding retains a cleanup-only canonical lease when disposal fails', async t => {
  const r = await setup(t); r.failBind(); r.failCleanup(true);
  const result = await r.invoke('loom.fixture.acquire', r.input);
  assert.equal(result.availability, 'error'); assert.equal(JSON.stringify(result).includes('private-bind-token'), false);
  const owned = [...r.context.resources.keys()].filter(key => key.startsWith(fixturesKey + ':'));
  assert.equal(owned.length, 1);
  const fixture = r.context.resources.get(owned[0]!) as { expiresAtUtcMs: number };
  assert.equal(fixture.expiresAtUtcMs, 0);
  r.failCleanup(false); await disposeFixtures(r.context); assert.equal(r.context.resources.has(owned[0]!), false);
});
