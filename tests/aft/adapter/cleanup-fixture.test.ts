import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CapabilityRegistry, createCapabilityContext, getRegisteredResource, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { evidenceKey, getFixtureEvidenceStore } from './evidence.js';
import { disposeFixtures, fixturesKey, getFixture, getFixtureAuthority, putCleanupFixture, putFixture, releaseFixture } from './ownership.js';

function setup(dispose: () => Promise<void> = async () => {}) {
  const context = createCapabilityContext({ file: 'cleanup.test.yaml', line: 1 }, new CapabilityRegistry());
  const owner = { leaseId: 'partial-lease', runId: context.runId, suiteId: context.suiteId,
    scope: context.scope, caseId: context.caseId, profile: 'deterministic', dispose };
  return { context, owner, key: `${fixturesKey}:${owner.leaseId}` };
}

test('partial cleanup registers without failure evidence and cannot grant observation authority', async () => {
  let disposed = 0;
  const h = setup(async () => { disposed++; });
  const fixture = putCleanupFixture(h.context, h.owner);
  assert.equal(getRegisteredResource(h.context, h.key, h.owner.leaseId), fixture);
  assert.deepEqual([...h.context.resources.keys()], [h.key]);
  assert.equal(h.context.resources.has(evidenceKey), false);
  assert.throws(() => getFixtureEvidenceStore(h.context, h.owner.leaseId));
  assert.throws(() => getFixtureAuthority(h.context, h.owner.leaseId));
  await assert.rejects(getFixture(h.context, h.owner.leaseId));
  await assert.rejects(fixture.readApi('/api/agents', h.context.signal));
  await assert.rejects(fixture.readFiles('/api/files', h.context.signal));
  await assert.rejects(fixture.resolveAgent('agt_foreign', h.context.signal));
  assert.equal(fixture.operationAuthority, undefined);
  assert.throws(() => { fixture.expiresAtUtcMs = Number.MAX_SAFE_INTEGER; });
  assert.throws(() => putFixture(h.context, { ...fixture, leaseId: 'upgrade' }));
  await releaseFixture(h.context, h.owner.leaseId);
  assert.equal(disposed, 1);
  assert.equal(h.context.resources.has(h.key), false);
});

test('failed disposal retains exact cleanup authority for retry after cancellation', async () => {
  let attempts = 0;
  const h = setup(async () => { if (++attempts === 1) throw new Error('owned close failed'); });
  const fixture = putCleanupFixture(h.context, h.owner);
  await assert.rejects(releaseFixture(h.context, h.owner.leaseId), /owned close failed/);
  assert.equal(h.context.resources.get(h.key), fixture);
  const cancelled = new AbortController();
  cancelled.abort();
  h.context.signal = cancelled.signal;
  revokeCapabilityContext(h.context);
  assert.equal(h.context.signal.aborted, true);
  await disposeFixtures(h.context);
  assert.equal(attempts, 2);
  assert.equal(h.context.resources.has(h.key), false);
});

test('foreign and duplicate registration cannot overwrite the owned cleanup callback', async () => {
  let disposed = 0;
  const h = setup(async () => { disposed++; });
  for (const changed of [{ runId: 'foreign' }, { suiteId: 'foreign' }, { caseId: 'foreign' }, { scope: 'suite' as const }]) {
    assert.throws(() => putCleanupFixture(h.context, { ...h.owner, ...changed }));
    assert.equal(h.context.resources.size, 0);
  }
  const fixture = putCleanupFixture(h.context, h.owner);
  assert.throws(() => putCleanupFixture(h.context, h.owner));
  assert.equal(h.context.resources.get(h.key), fixture);
  await disposeFixtures(h.context);
  assert.equal(disposed, 1);
});

test('failed canonical registration rolls back only its exact resource and preserves replacement', () => {
  for (const replace of [false, true]) {
    const h = setup();
    const foreign = { foreign: true };
    h.context.resources.set('unrelated', foreign);
    const replacement = { replacement: true };
    h.context.registerResource = () => {
      if (replace) h.context.resources.set(h.key, replacement);
      throw new Error('registration denied');
    };
    assert.throws(() => putCleanupFixture(h.context, h.owner), /registration denied/);
    assert.equal(h.context.resources.get(h.key), replace ? replacement : undefined);
    assert.equal(h.context.resources.get('unrelated'), foreign);
    assert.equal(h.context.resources.has(evidenceKey), false);
  }
});

test('replacement during awaited cleanup is preserved and cannot publish release success', async () => {
  let enter!: () => void, finish!: () => void;
  const entered = new Promise<void>(resolve => { enter = resolve; });
  const pending = new Promise<void>(resolve => { finish = resolve; });
  const h = setup(async () => { enter(); await pending; });
  putCleanupFixture(h.context, h.owner);
  const releasing = releaseFixture(h.context, h.owner.leaseId);
  await entered;
  const replacement = { foreign: true };
  h.context.resources.set(h.key, replacement);
  finish();
  await assert.rejects(releasing, /replaced during disposal/);
  assert.equal(h.context.resources.get(h.key), replacement);
});

test('revoked context cannot acquire new cleanup registration', () => {
  const h = setup();
  revokeCapabilityContext(h.context);
  assert.throws(() => putCleanupFixture(h.context, h.owner));
  assert.equal(h.context.resources.size, 0);
});
