import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm, readFile, readdir, lstat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { CapabilityRegistry, createCapabilityContext, getRegisteredResource, calculateImplementationPin, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createEvidenceStore, putEvidenceStore, evidenceKey } from '../evidence.js';
import { getFixture, fixturesKey, disposeFixtures } from '../ownership.js';
import { createFixtureProviders, type FixtureProviderOptions } from './providers.js';
import { ObservationError } from '../protocol.js';
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
  const plan: FixturePlan = { profile: 'agents-real-opencode', loomRevision: { ...revision }, fleetRevision: { ...revision }, engineRevision: { ...revision }, adapterRevision: { ...revision },
    model: 'openai/model', caseCount: 1, maxCases: 10, selectionSha256: 'e'.repeat(64), leaseDurationMs: 10000 };
  let driverCalls = 0; let provisionFails = false; let cleanupFails = false; let incomplete = false; let changed = false; let bindFails = false; let bindError: Error | undefined; let releaseArtifactFails = false;
  let beforeRemove: (() => Promise<void>) | undefined;
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
    async remove(resource: Resource) { if (beforeRemove) await beforeRemove(); calls.push(`remove:${resource.id}`); if (cleanupFails) throw new Error('password=secret-cleanup-token'); },
    async artifact(kind, value) { if (kind === 'release' && releaseArtifactFails) throw new Error('Bearer private-release-artifact-token'); return { ...await store.retain(JSON.stringify({ kind, value })), redaction: 'sanitized' as const }; },
  };
  const options: FixtureProviderOptions = { implementation: pin, implementationSha256: pin.sha256, plans: [plan],
    driver() { driverCalls++; return driver; }, evidenceAfterFailure: async () => store,
    async bind() { if (bindFails) throw bindError ?? new Error("Bearer private-bind-token"); const stat = await lstat(evidenceRoot); return { fixtureRunId:'af12345678',evidenceClass: 'deterministic', evidenceStore: store,
      roots: new Map([['runtime', { path: evidenceRoot, device: stat.dev, inode: stat.ino }]]), secrets: [],
      readApi: async () => ({ status: 200, body: {} }), readFiles: async () => ({ status: 200, body: {} }),
      resolveAgent: async () => { throw new Error('No test agent'); } }; },
  };
  const registry = new CapabilityRegistry(); for (const provider of createFixtureProviders(options)) registry.register(provider);
  const context = createCapabilityContext({ file: 'fixture.test.yaml', line: 1 }, registry, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002');
  putEvidenceStore(context, store);
  const input = { runId: context.runId, profile: plan.profile, loomRevision: { ...revision }, fleetRevision: { ...revision }, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 };
  const invoke = (id: string, data: unknown, ctx: CapabilityContext = context) => registry.invoke({ id, version: 1, input: {} }, data, ctx);
  return { registry, context, input, invoke, store, options, calls, get driverCalls() { return driverCalls; },
    failBind(error?: Error) { bindFails = true; bindError = error; }, failReleaseArtifact(value: boolean) { releaseArtifactFails = value; }, beforeRemove(callback: () => Promise<void>) { beforeRemove = callback; }, failProvision() { provisionFails = true; }, failCleanup(value: boolean) { cleanupFails = value; }, incomplete() { incomplete = true; }, changeSource() { changed = true; } };
}
function leaseId(result: Awaited<ReturnType<CapabilityRegistry['invoke']>>) {
  assert.equal(result.availability, 'observed'); return (result.data as { lease: { id: string } }).lease.id;
}
test('fixture operations use the canonical store and retained output receipt without owner tokens', async t => {
  const r = await setup(t); const result = await r.invoke('loom.fixture.acquire', r.input); const id = leaseId(result);
  const fixture = await getFixture(r.context, id);
  assert.equal(fixture.leaseId, id); assert.equal(r.context.resources.get(`${fixturesKey}:${id}`), fixture);
  assert.equal(JSON.stringify(result).includes('ownerToken'), false);
  assert.equal((result.data as {fixtureRunId:string}).fixtureRunId,'af12345678');
  assert.notEqual((result.data as {fixtureRunId:string}).fixtureRunId,r.context.runId);
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
  for (const field of ['command', 'modulePath', 'code', 'leaseId', 'browser','fixtureRunId']) await assert.rejects(r.invoke('loom.fixture.acquire', { ...r.input, [field]: 'unsafe' }));
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


test('binding failure survives failed release receipt and retries exact canonical disposal', async t => {
  const r = await setup(t); r.failBind(new ObservationError('source-mismatch', 'Fixture binding source mismatch'));
  r.failCleanup(true); r.failReleaseArtifact(true);
  const result = await r.invoke('loom.fixture.acquire', r.input);
  assert.equal(result.availability, 'error'); assert.equal(result.error?.code, 'source-mismatch');
  assert.equal(JSON.stringify(result).includes('private-release-artifact-token'), false);
  const keys = [...r.context.resources.keys()].filter(key => key.startsWith(fixturesKey + ':'));
  assert.equal(keys.length, 1);
  const id = keys[0]!.slice(fixturesKey.length + 1);
  assert.equal(getRegisteredResource(r.context, keys[0]!, id), r.context.resources.get(keys[0]!));
  assert.equal((await r.invoke('loom.fixture.observe', { leaseId: id })).availability, 'error');
  r.failCleanup(false);
  await assert.rejects(disposeFixtures(r.context));
  assert.equal(r.context.resources.has(keys[0]!), true);
  const removed = r.calls.filter(call => call.startsWith('remove:')).length;
  r.failReleaseArtifact(false); await disposeFixtures(r.context);
  assert.equal(r.context.resources.has(keys[0]!), false);
  assert.equal(r.calls.filter(call => call.startsWith('remove:')).length, removed);
});

test('binding failure registers cleanup ownership before awaiting disposal and preserves foreign replacement', async t => {
  const r = await setup(t); r.failBind();
  let enter!: () => void; const entered = new Promise<void>(resolve => { enter = resolve; });
  let finish!: () => void; const pending = new Promise<void>(resolve => { finish = resolve; });
  r.beforeRemove(async () => { enter(); await pending; });
  const acquisition = r.invoke('loom.fixture.acquire', r.input);
  await entered;
  try {
    const keys = [...r.context.resources.keys()].filter(key => key.startsWith(fixturesKey + ':'));
    assert.equal(keys.length, 1);
    const original = r.context.resources.get(keys[0]!) as { runId: string; dispose(): Promise<void> };
    let foreignDisposals = 0;
    const replacement = { ...original, runId: 'foreign-run', dispose: async () => { foreignDisposals++; } };
    r.context.resources.set(keys[0]!, replacement);
    finish(); const result = await acquisition;
    assert.equal(result.availability, 'error');
    assert.equal(r.context.resources.get(keys[0]!), replacement);
    await disposeFixtures(r.context); assert.equal(foreignDisposals, 0);
    assert.equal(r.calls.filter(call => call === 'remove:compose').length, 1);
  } finally { finish(); await acquisition.catch(() => {}); }
});

test('probe evidence recovery failure must retain cleanup ownership without a store', async t => {
  const r = await setup(t);
  r.context.resources.delete(evidenceKey);
  r.options.evidenceAfterFailure = async () => { throw new Error('Bearer private-store-recovery-token'); };
  r.failBind(new ObservationError('source-mismatch', 'Fixture binding source mismatch'));
  r.failCleanup(true);
  const result = await r.invoke('loom.fixture.acquire', r.input);
  assert.equal(result.error?.code, 'source-mismatch');
  assert.equal(JSON.stringify(result).includes('private-store-recovery-token'), false);
  const keys = [...r.context.resources.keys()].filter(key => key.startsWith(fixturesKey + ':'));
  assert.equal(keys.length, 1);
  r.failCleanup(false); await disposeFixtures(r.context);
});


test('cleanup registers before failure evidence recovery waits and remains retryable after abort', async t => {
  const r = await setup(t); r.context.resources.delete(evidenceKey);
  r.failBind(new ObservationError('source-mismatch', 'Fixture binding source mismatch')); r.failCleanup(true);
  let enter!: () => void; const entered = new Promise<void>(resolve => { enter = resolve; });
  let finish!: () => void; const pending = new Promise<void>(resolve => { finish = resolve; });
  r.options.evidenceAfterFailure = async () => { enter(); await pending; throw new Error('Bearer private-evidence-token'); };
  const acquisition = r.invoke('loom.fixture.acquire', r.input);
  await entered;
  try {
    const keys = [...r.context.resources.keys()].filter(key => key.startsWith(fixturesKey + ':'));
    assert.equal(keys.length, 1);
    const id = keys[0]!.slice(fixturesKey.length + 1);
    const fixture = getRegisteredResource(r.context, keys[0]!, id) as { cleanupOnly?: true };
    assert.equal(fixture.cleanupOnly, true); assert.equal(Object.isFrozen(fixture), true);
    assert.equal(r.context.resources.has(evidenceKey), false);
    assert.equal(r.context.resources.has(`${evidenceKey}:${id}`), false);
    const aborted = new AbortController(); aborted.abort(); r.context.signal = aborted.signal;
    finish(); await assert.rejects(acquisition, error => error === aborted.signal.reason);
    assert.equal(r.context.resources.get(keys[0]!), fixture);
    r.failCleanup(false); await disposeFixtures(r.context);
    assert.equal(r.context.resources.has(keys[0]!), false);
    assert.equal(r.calls.filter(call => call === 'remove:compose').length, 2);
  } finally { finish(); await acquisition.catch(() => {}); }
});


for (const flag of ['omitted', 'false'] as const) test(`public provider disposal refuses same-owner replacement with cleanupOnly ${flag}`, async t => {
  const r = await setup(t); r.failBind(); r.failCleanup(true);
  assert.equal((await r.invoke('loom.fixture.acquire', r.input)).availability, 'error');
  const key = [...r.context.resources.keys()].find(key => key.startsWith(fixturesKey + ':'));
  assert.ok(key);
  const original = r.context.resources.get(key)! as { cleanupOnly?: true; dispose(): Promise<void> };
  let foreignDisposals = 0;
  const replacement = { ...original, dispose: async () => { foreignDisposals++; } };
  if (flag === 'omitted') delete replacement.cleanupOnly;
  else Object.defineProperty(replacement, 'cleanupOnly', { value: false });
  r.context.resources.set(key, replacement);
  try {
    await assert.rejects(disposeFixtures(r.context));
    assert.equal(foreignDisposals, 0);
    assert.equal(r.context.resources.get(key), replacement);
  } finally {
    r.context.resources.set(key, original); r.failCleanup(false); await disposeFixtures(r.context);
  }
});

for (const revoked of [false, true]) test(`public provider cleanup cannot transfer into another context revoked=${revoked}`, async t => {
  const r = await setup(t); const foreign = await setup(t); r.failBind(); r.failCleanup(true);
  assert.equal((await r.invoke('loom.fixture.acquire', r.input)).availability, 'error');
  const key = [...r.context.resources.keys()].find(key => key.startsWith(fixturesKey + ':'));
  assert.ok(key);
  const original = r.context.resources.get(key)!;
  foreign.context.suiteId = r.context.suiteId; foreign.context.scope = r.context.scope; foreign.context.caseId = r.context.caseId;
  foreign.context.resources.set(key, original);
  if (revoked) { const abort = new AbortController(); abort.abort(); foreign.context.signal = abort.signal; }
  const before = r.calls.filter(call => call.startsWith('remove:')).length;
  try {
    await assert.rejects(disposeFixtures(foreign.context));
    assert.equal(r.calls.filter(call => call.startsWith('remove:')).length, before);
    assert.equal(r.context.resources.get(key), original);
    assert.equal(foreign.context.resources.get(key), original);
  } finally {
    foreign.context.resources.delete(key); r.failCleanup(false); await disposeFixtures(r.context);
  }
});
