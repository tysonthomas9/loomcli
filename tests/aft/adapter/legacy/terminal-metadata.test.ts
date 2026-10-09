import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createTerminalMetadataDetach, TerminalDetachFacts, type TerminalMetadataAccess } from './terminal-metadata.js';
import { LegacyError } from './operations.js';
import type { HttpResponse } from '../protocol.js';

const input = { leaseId: 'lease', workspaceId: 'WS', agentName: 'worker', expectedServeGeneration: 'serve-gen' };
const call = { runId: 'run', invocationId: 'case:0', signal: new AbortController().signal };
const tab = (session_name: string, agent_id = 'worker') => ({ session_name, agent_id });
function setup(initial: HttpResponse['body'], final: HttpResponse['body'] = { data: [] }) {
  let reads = 0, generation = 'serve-gen', status = 204, throwDelete = false;
  let deleted = 0;
  const effects: string[] = [];
  const access: TerminalMetadataAccess = {
    async assertOwned(value) {
      if (value.leaseId !== 'lease' || value.workspaceId !== 'WS' || value.agentName !== 'worker')
        throw new LegacyError('ownership-mismatch', 'Injected foreign identity');
      if (value.expectedServeGeneration !== generation) throw new LegacyError('stale-generation', 'Injected generation changed');
      return { leaseId: 'lease', runId: 'run', evidenceClass: 'deterministic', secrets: [] };
    },
    async readTabs(value) {
      await access.assertOwned(value, call); effects.push('GET');
      return { status: 200, body: ++reads > 2 ? final : initial };
    },
    async deleteCapturedTab(value, session) {
      await access.assertOwned(value, call); effects.push(`DELETE:${session}`); deleted++;
      if (throwDelete) throw new Error('private-transport-detail');
      return { status, body: null };
    },
  };
  return { access, detach: createTerminalMetadataDetach(access), effects, deleted: () => deleted,
    status: (value: number) => { status = value; }, generation: (value: string) => { generation = value; },
    throwDelete: () => { throwDelete = true; } };
}
test('frozen helper actors preserve every matching session, foreign tabs and duplicate order', async () => {
  const s = setup({ data: [tab('first'), tab('foreign', 'other'), null, {}, tab('first'), tab('last'), tab('')] });
  const result = await s.detach(input, call);
  assert.deepEqual(s.effects, ['GET', 'GET', 'DELETE:first', 'DELETE:first', 'DELETE:last', 'GET']);
  assert.deepEqual(result.facts, {
    kind: 'terminal-metadata-detached', workspaceId: 'WS', agentName: 'worker', serve: { id: 'serve', generation: 'serve-gen' },
    capturedSessions: ['first', 'first', 'last'], deletions: [
      { sessionName: 'first', status: 204, outcome: 'http-success' },
      { sessionName: 'first', status: 204, outcome: 'http-success' },
      { sessionName: 'last', status: 204, outcome: 'http-success' }], remainingSessions: [], complete: true,
  });
  assert.equal('afterGeneration' in result.facts, false);
  assert.equal(TerminalDetachFacts.safeParse({ ...result.facts, processExited: true }).success, false);
  await assert.rejects(s.detach(input, call), { code: 'mutation-repeated' }); assert.equal(s.deleted(), 3);
});
test('zero matching tabs requires a final reachable read for both original response shapes', async () => {
  for (const body of [[], { data: [tab('other', 'foreign')] }]) {
    const s = setup(body); const result = await s.detach(input, call);
    assert.deepEqual(s.effects, ['GET', 'GET', 'GET']); assert.deepEqual(result.facts.capturedSessions, []);
    assert.deepEqual(result.facts.remainingSessions, []);
  }
});
test('delete failures and remaining tabs are observed facts for YAML, not a successful-stop oracle', async () => {
  const s = setup([tab('first'), tab('second')], { data: [tab('second'), tab('third')] }); s.status(503);
  const result = await s.detach(input, call);
  assert.deepEqual(result.facts.deletions, [{ sessionName: 'first', status: 503, outcome: 'http-failure' },
    { sessionName: 'second', status: 503, outcome: 'http-failure' }]);
  assert.deepEqual(result.facts.remainingSessions, ['second', 'third']); assert.equal(s.deleted(), 2);
  assert.equal('allSuccess' in result.facts, false);
});
test('transport delete uncertainty is retained once without a retry or private error text', async () => {
  const s = setup([tab('first')]); s.throwDelete(); const result = await s.detach(input, call);
  assert.deepEqual(result.facts.deletions, [{ sessionName: 'first', status: null, outcome: 'transport-error' }]);
  assert.equal(JSON.stringify(result).includes('private-transport-detail'), false);
  await assert.rejects(s.detach(input, call), { code: 'mutation-repeated' }); assert.equal(s.deleted(), 1);
});
test('foreign identities, unsafe input and wrong retained generation have zero effects', async () => {
  const s = setup([tab('first')]);
  let sequence = 0;
  for (const patch of [{ leaseId: 'other' }, { workspaceId: 'foreign' }, { agentName: 'foreign' },
    { expectedServeGeneration: 'old' }, { workspaceId: '../WS' }, { sessionName: 'arbitrary' }])
    await assert.rejects(s.detach({ ...input, ...patch }, { ...call, invocationId: `invalid:${sequence++}` }));
  await assert.rejects(s.detach(input, { ...call, runId: 'foreign', invocationId: 'foreign-run' }), { code: 'ownership-mismatch' });
  assert.deepEqual(s.effects, []);
});
test('same invocation is reserved before deferred reads so concurrent callers cannot duplicate DELETE', async () => {
  const s = setup([tab('first')]); const read = s.access.readTabs;
  let release!: () => void, entered!: () => void, reads = 0;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const ready = new Promise<void>(resolve => { entered = resolve; });
  s.access.readTabs = async (...args) => { if (++reads === 1) { entered(); await gate; } return read(...args); };
  const first = s.detach(input, call); await ready;
  const second = s.detach(input, call); release();
  const results = await Promise.allSettled([first, second]);
  assert.deepEqual(results.map(result => result.status), ['fulfilled', 'rejected']);
  assert.equal(results[1]!.status === 'rejected' && results[1]!.reason.code, 'mutation-repeated');
  assert.equal(s.deleted(), 1); assert.equal(reads, 3);
});
test('uncertain DELETE consumes the invocation during and after a deferred transport failure', async () => {
  const s = setup([tab('first')]); let release!: () => void, entered!: () => void, attempts = 0;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const ready = new Promise<void>(resolve => { entered = resolve; });
  s.access.deleteCapturedTab = async () => { attempts++; entered(); await gate; throw new Error('private-uncertain-delete'); };
  const first = s.detach(input, call); await ready;
  const second = s.detach(input, call); release();
  const results = await Promise.allSettled([first, second]);
  assert.deepEqual(results.map(result => result.status), ['fulfilled', 'rejected']);
  assert.equal(results[0]!.status === 'fulfilled' && results[0]!.value.facts.deletions[0]!.outcome, 'transport-error');
  await assert.rejects(s.detach(input, call), { code: 'mutation-repeated' }); assert.equal(attempts, 1);
});
test('well-formed pre-effect read failures consume their invocation without a same-ID replay', async () => {
  const s = setup([]); let reads = 0;
  s.access.readTabs = async () => { reads++; throw new Error('injected unavailable read'); };
  await assert.rejects(s.detach(input, call));
  s.access.readTabs = async () => { reads++; return { status: 200, body: [] }; };
  await assert.rejects(s.detach(input, call), { code: 'mutation-repeated' });
  assert.equal(reads, 1); assert.equal(s.deleted(), 0);
});
test('malformed, oversized, unsafe matching or incomplete list never proves absence or mutates', async () => {
  const invalidBodies: HttpResponse['body'][] = [{}, { data: null }, { data: {} }, [tab('../escape')], [tab('unsafe space')]];
  for (const body of invalidBodies) {
    const s = setup(body); await assert.rejects(s.detach(input, call), { code: 'response-invalid' }); assert.equal(s.deleted(), 0);
  }
});
test('overflow and partial page markers remain incomplete before any mutation', async () => {
  const bodies: HttpResponse['body'][] = [Array.from({ length: 129 }, (_, index) => tab(`s-${index}`)), Array(1001).fill(null),
    { data: [], total: 1 }, { data: [], has_more: true }];
  for (const body of bodies) {
    const s = setup(body); await assert.rejects(s.detach(input, call), { code: 'incomplete-pages' }); assert.equal(s.deleted(), 0);
  }
});
test('the initial reachability probe does not replace the subsequent fresh list', async () => {
  const s = setup([tab('first')]); const read = s.access.readTabs; let count = 0;
  s.access.readTabs = async (...args) => { const result = await read(...args); return ++count === 1 ? { status: 200, body: {} } : result; };
  const result = await s.detach(input, call); assert.deepEqual(result.facts.capturedSessions, ['first']); assert.equal(s.deleted(), 1);
});
test('typed detach intentionally closes the original masked intermediate-enumeration failure', async () => {
  const s = setup([], []); let reads = 0;
  s.access.readTabs = async () => ({ status: 200, body: ++reads === 2 ? {} : [] });
  await assert.rejects(s.detach(input, call), { code: 'response-invalid' });
  assert.equal(reads, 2); assert.equal(s.deleted(), 0);
});
test('unreachable initial and final read fail without inventing an empty list', async () => {
  const first = setup([]); first.access.readTabs = async () => ({ status: 503, body: [] });
  await assert.rejects(first.detach(input, call), { code: 'response-invalid' }); assert.equal(first.deleted(), 0);
  const final = setup([tab('first')]); const read = final.access.readTabs; let count = 0;
  final.access.readTabs = async (...args) => ++count === 3 ? { status: 503, body: [] } : read(...args);
  await assert.rejects(final.detach(input, call), { code: 'response-invalid' }); assert.equal(final.deleted(), 1);
});
test('generation replacement before a captured delete prevents mutation and final claims', async () => {
  const s = setup([tab('first')]); const read = s.access.readTabs; let count = 0;
  s.access.readTabs = async (...args) => { const result = await read(...args); if (++count === 2) s.generation('replacement'); return result; };
  await assert.rejects(s.detach(input, call), { code: 'stale-generation' }); assert.equal(s.deleted(), 0);
  const response = setup([tab('first')]); const remove = response.access.deleteCapturedTab;
  response.access.deleteCapturedTab = async (...args) => { const result = await remove(...args); response.generation('replacement'); return result; };
  await assert.rejects(response.detach(input, call), { code: 'stale-generation' }); assert.equal(response.deleted(), 1);
});
