import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { FixtureLifecycle, FixtureError, type FixtureDriver, type FixturePlan, type Resource, type Inventory, type AcquireRequest } from './lifecycle.js';

const revision = { repository: 'loom', commit: 'a'.repeat(40), tree: 'b'.repeat(40),
  sourceManifestSha256: 'c'.repeat(64), buildManifestSha256: 'd'.repeat(64) };
const plan: FixturePlan = { profile: 'agents-real-opencode', loomRevision: revision, fleetRevision: { ...revision, repository: 'fleet' },
  engineRevision: { ...revision, repository: 'engine' }, adapterRevision: revision, model: 'openai/model',
  maxCases: 10, caseCount: 9, selectionSha256: 'e'.repeat(64), leaseDurationMs: 1000 };
const input: AcquireRequest = { runId: 'run', profile: plan.profile, loomRevision: plan.loomRevision, fleetRevision: plan.fleetRevision,
  model: plan.model, maxCases: 9, selectionSha256: plan.selectionSha256 };
function rig() {
  const calls: string[] = [];
  const artifacts: unknown[] = [];
  const removed: string[] = [];
  let clock = 1000;
  let sourceMatches = true;
  let provisionFails = false;
  let allocationFails = false;
  let removeFails = '';
  let foreign = '';
  let incomplete = '';
  let gate: Promise<void> | undefined;
  const resources: Resource[] = [
    { id: 'owned-directory', kind: 'directory', generation: 'dev:ino' },
    { id: 'owned-lock', kind: 'lock', generation: 'dev:ino' },
    { id: 'owned-ports', kind: 'ports', generation: 'lease' },
    { id: 'owned-compose', kind: 'compose', generation: 'lease' },
  ];
  const driver: FixtureDriver = {
    async preflight() { calls.push('preflight'); },
    async identity() { calls.push('identity'); return sourceMatches; },
    async allocate(_id, _run, record) {
      calls.push('allocate'); record(resources[0]!); record(resources[1]!);
      if (allocationFails) throw new Error('Bearer private-allocation-token');
      record(resources[2]!);
    },
    async provision(_plan, record) {
      calls.push('provision'); record(resources[3]!);
      if (gate) await gate;
      if (provisionFails) throw new Error('password=private-provision-token');
      return { apiOrigin: 'http://127.0.0.1:4001', filesOrigin: 'http://127.0.0.1:4002', workspaceId: 'LOCALMODE', repo: '/owned/repo' };
    },
    async inspect(resource): Promise<Inventory> {
      calls.push(`inspect:${resource.id}`);
      return { complete: resource.id !== incomplete, owned: resource.id !== foreign,
        services: resource.kind === 'compose' ? [{ id: 'serve', pid: 43, generation: 'exact-generation', state: 'running' }] : [] };
    },
    async remove(resource) { calls.push(`remove:${resource.id}`); if (resource.id === removeFails) throw new Error('secret=private-cleanup-token'); removed.push(resource.id); },
    async artifact(kind, value) {
      calls.push(`artifact:${kind}`); artifacts.push(value);
      const bytes = Buffer.from(JSON.stringify(value));
      return { id: `${kind}.json`, sha256: createHash('sha256').update(bytes).digest('hex'), bytes: bytes.length,
        mediaType: 'application/json', redaction: 'sanitized' };
    },
  };
  let created = 0;
  const manager = new FixtureLifecycle([plan], () => { created++; return driver; }, () => clock, () => `opaque-${created}`);
  return { manager, driver, calls, artifacts, removed, get created() { return created; },
    setClock(value: number) { clock = value; }, setSource(value: boolean) { sourceMatches = value; },
    failProvision() { provisionFails = true; }, failAllocation() { allocationFails = true; },
    failRemove(id: string) { removeFails = id; }, foreign(id: string) { foreign = id; }, incomplete(id: string) { incomplete = id; },
    gate(value: Promise<void>) { gate = value; } };
}
const signal = () => new AbortController().signal;

test('acquisition mints an opaque lease without an existing lease; observes and releases exact resources', async () => {
  const r = rig(); const result = await r.manager.acquire(input, signal());
  assert.equal(result.lease.id, 'opaque-1');
  assert.equal('ownerToken' in result.lease, false);
  assert.equal(result.lease.expiresAt, '1970-01-01T00:00:02.000Z');
  assert.equal(result.browserLeaseId, result.lease.id);
  const observation = await r.manager.observe(result.lease.id, 'run');
  assert.equal(observation.owned, true); assert.equal(observation.services[0]?.generation, 'exact-generation');
  const released = await r.manager.release(result.lease.id, 'run');
  assert.equal(released.released, true);
  assert.deepEqual(r.removed, ['owned-compose', 'owned-ports', 'owned-lock', 'owned-directory']);
  assert.equal((await r.manager.release(result.lease.id, 'run')).released, true);
});

test('explicit observation signal is the same instance at identity, preparation and each resource', async () => {
  const r = rig(), acquired = await r.manager.acquire(input, signal()), controller = new AbortController();
  const received: (AbortSignal | undefined)[] = [];
  const identity = r.driver.identity.bind(r.driver), inspect = r.driver.inspect.bind(r.driver);
  r.driver.identity = async (plan, signal) => { received.push(signal); return identity(plan, signal); };
  r.driver.prepareObserve = async signal => { received.push(signal); };
  r.driver.inspect = async (resource, signal) => { received.push(signal); return inspect(resource, signal); };
  assert.equal((await r.manager.observe(acquired.lease.id, 'run', controller.signal)).owned, true);
  assert.equal(received.length, 6); assert.ok(received.every(actual => actual === controller.signal));
});

test('preaborted observation signal invokes no identity, preparation, inspection or artifact', async () => {
  const r = rig(), acquired = await r.manager.acquire(input, signal()), controller = new AbortController();
  const before = [...r.calls]; controller.abort();
  await assert.rejects(r.manager.observe(acquired.lease.id, 'run', controller.signal));
  assert.deepEqual(r.calls, before); assert.equal((await r.manager.release(acquired.lease.id, 'run')).released, true);
});

for (const stage of ['identity', 'prepare', 'inspect', 'artifact'] as const) test(`abort during ${stage} denies observation and leaves cleanup available`, async () => {
  const r = rig(), acquired = await r.manager.acquire(input, signal()), controller = new AbortController();
  const before = r.calls.length;
  if (stage === 'identity') r.driver.identity = async () => { controller.abort(); return true; };
  if (stage === 'prepare') r.driver.prepareObserve = async () => { controller.abort(); };
  if (stage === 'inspect') {
    const inspect = r.driver.inspect.bind(r.driver);
    r.driver.inspect = async (resource, signal) => { controller.abort(); return inspect(resource, signal); };
  }
  if (stage === 'artifact') {
    const artifact = r.driver.artifact.bind(r.driver);
    r.driver.artifact = async (kind, value) => { if (kind === 'observe') controller.abort(); return artifact(kind, value); };
  }
  await assert.rejects(r.manager.observe(acquired.lease.id, 'run', controller.signal));
  const dispatched = r.calls.slice(before);
  if (stage === 'identity' || stage === 'prepare') assert.equal(dispatched.some(call => call.startsWith('inspect:')), false);
  if (stage === 'inspect') assert.equal(dispatched.filter(call => call.startsWith('inspect:')).length, 1);
  if (stage !== 'artifact') assert.equal(dispatched.includes('artifact:observe'), false);
  assert.equal((await r.manager.release(acquired.lease.id, 'run')).released, true);
});

for (const [name, change, code] of [
  ['source commit', { loomRevision: { ...revision, commit: 'f'.repeat(40) } }, 'source-mismatch'],
  ['source manifest', { loomRevision: { ...revision, sourceManifestSha256: 'f'.repeat(64) } }, 'source-mismatch'],
  ['build identity', { loomRevision: { ...revision, buildManifestSha256: 'f'.repeat(64) } }, 'source-mismatch'],
  ['fleet identity', { fleetRevision: revision }, 'source-mismatch'],
  ['model', { model: 'aft/m' }, 'identity-mismatch'],
  ['selection hash', { selectionSha256: 'f'.repeat(64) }, 'identity-mismatch'],
  ['under cap', { maxCases: 8 }, 'identity-mismatch'],
  ['over cap', { maxCases: 11 }, 'identity-mismatch'],
  ['fractional cap', { maxCases: 9.5 }, 'identity-mismatch'],
  ['profile not registered by this runner', { profile: 'legacy-real-codex' }, 'unsupported-capability'],
] as const) test(`${name} fails before driver/auth/build allocation`, async () => {
  const r = rig();
  await assert.rejects(r.manager.acquire({ ...input, ...change }, signal()), (error: unknown) => error instanceof FixtureError && error.code === code);
  assert.equal(r.created, 0); assert.deepEqual(r.calls, []);
});

test('registered source that changed on disk fails before allocation', async () => {
  const r = rig(); r.setSource(false);
  await assert.rejects(r.manager.acquire(input, signal()), FixtureError);
  assert.deepEqual(r.calls, ['preflight', 'identity']);
});
test('partial allocation failure reverses the recorded resources and retains safe diagnostics', async () => {
  const r = rig(); r.failAllocation();
  await assert.rejects(r.manager.acquire(input, signal()), (error: unknown) => error instanceof FixtureError && !!error.receipt && error.remainingOwnedResources.length === 0);
  assert.deepEqual(r.removed, ['owned-lock', 'owned-directory']);
  assert.equal(JSON.stringify(r.artifacts).includes('private-allocation-token'), false);
});
test('failed provisioning cleans resources that were recorded before the failed start', async () => {
  const r = rig(); r.failProvision();
  await assert.rejects(r.manager.acquire(input, signal()), FixtureError);
  assert.deepEqual(r.removed, ['owned-compose', 'owned-ports', 'owned-lock', 'owned-directory']);
  assert.equal(JSON.stringify(r.artifacts).includes('private-provision-token'), false);
});
test('foreign project cannot be removed; dependent ports/locks/state remain reserved', async () => {
  const r = rig(); r.failProvision(); r.foreign('owned-compose');
  await assert.rejects(r.manager.acquire(input, signal()), (error: unknown) => error instanceof FixtureError && error.remainingOwnedResources.length === 4);
  assert.deepEqual(r.removed, []);
});
test('cleanup failure returns a safe receipt and retains resources for exact retry', async () => {
  const r = rig(); const acquired = await r.manager.acquire(input, signal());
  r.failRemove('owned-compose');
  const failed = await r.manager.release(acquired.lease.id, 'run');
  assert.equal(failed.released, false); assert.equal(failed.remainingOwnedResources.length, 4);
  assert.equal(JSON.stringify(r.artifacts).includes('private-cleanup-token'), false);
  r.failRemove('');
  assert.equal((await r.manager.release(acquired.lease.id, 'run')).released, true);
});
test('incomplete inventory cannot satisfy ownership or absence/cardinality assertions', async () => {
  const r = rig(); const acquired = await r.manager.acquire(input, signal()); r.incomplete('owned-compose');
  await assert.rejects(r.manager.observe(acquired.lease.id, 'run'), FixtureError);
});
test('source change after acquisition rejects observation while cleanup stays available', async () => {
  const r = rig(); const acquired = await r.manager.acquire(input, signal()); r.setSource(false);
  await assert.rejects(r.manager.observe(acquired.lease.id, 'run'), FixtureError);
  assert.equal((await r.manager.release(acquired.lease.id, 'run')).released, true);
});
test('foreign run and expired lease cannot observe; expiry does not prevent cleanup', async () => {
  const r = rig(); const acquired = await r.manager.acquire(input, signal());
  await assert.rejects(r.manager.observe(acquired.lease.id, 'foreign'), FixtureError);
  await assert.rejects(r.manager.release(acquired.lease.id, 'foreign'), FixtureError);
  r.setClock(2000);
  await assert.rejects(r.manager.observe(acquired.lease.id, 'run'), FixtureError);
  assert.equal((await r.manager.release(acquired.lease.id, 'run')).released, true);
});
test('aborted request before allocation has no effects', async () => {
  const r = rig(); const controller = new AbortController(); controller.abort();
  await assert.rejects(r.manager.acquire(input, controller.signal)); assert.equal(r.created, 0);
});
test('cancellation after partial start still drains cleanup', async () => {
  const r = rig(); let drain!: () => void; const gate = new Promise<void>(resolve => { drain = resolve; }); r.gate(gate);
  const controller = new AbortController(); const acquired = r.manager.acquire(input, controller.signal);
  controller.abort(); drain(); await assert.rejects(acquired);
  assert.ok(!r.calls.includes('provision') || r.removed.includes('owned-compose'));
});
