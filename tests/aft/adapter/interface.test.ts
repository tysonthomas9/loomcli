import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { readdir, mkdtemp, realpath, rm, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin } from '@tysonthomas9/aft/capabilities';
import { createCoreProviders } from './index.js';
import { putFixture, getFixture, disposeFixtures, type OwnedFixture } from './ownership.js';
import { AgentRow, type Json } from './protocol.js';

async function setup(t: { after(fn: () => Promise<void>): void }) {
  const root = fileURLToPath(new URL('.', import.meta.url));
  const files = (await readdir(root)).filter(name => name.endsWith('.ts') && !name.endsWith('.test.ts'));
  const pin = calculateImplementationPin(root, files, 'index.ts', 'createCoreProviders');
  const registry = new CapabilityRegistry();
  for (const provider of createCoreProviders(pin)) registry.register(provider);
  const context = createCapabilityContext({ file: 'deterministic.test.yaml', line: 1 }, registry, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002');
  const evidenceRoot = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-adapter-evidence-')));
  t.after(() => rm(evidenceRoot, { recursive: true }));
  const evidenceStore = await createEvidenceStore(evidenceRoot);
  putEvidenceStore(context, evidenceStore);
  const row = AgentRow.parse({ agent_id: 'agt_owned', workspace_id: 'workspace', repo: '/owned/source', worktree_path: '/owned/tree',
    branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '', parent_agent_id: null,
    root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead', revision: 1, state: 'idle', running_turn_id: null,
    deleted_at: null, history_purged_at: null });
  let disposed = 0;
  let reads = 0;
  let payload: Json = { text: 'independently observed value', password: 'private-password' };
  const fixture: OwnedFixture = { leaseId: 'lease', runId: context.runId, caseId: context.caseId, suiteId: context.suiteId, scope: context.scope, workspaceId: 'workspace', repo: '/owned/source',
    profile: 'deterministic', expiresAtUtcMs: Date.now() + 100000, evidenceClass: 'deterministic', roots: new Map(),
    agents: new Map([['agt_owned', { row, commonDir: '/owned/source/.git' }]]), secrets: ['private-password'],
    readApi: async () => { reads++; return { status: 200, body: { events: [{ agent_id: row.agent_id, seq: 1, event_id: 'event_1',
      kind: 'item.completed', turn_id: 'turn_1', payload, created_at: '2026-10-09T00:00:00Z' }], snapshot_seq: 1, next: 1, more: false } }; },
    readFiles: async () => ({ status: 404, body: {} }), resolveAgent: async () => ({ row, commonDir: '/owned/source/.git' }),
    verify: async () => {}, dispose: async () => { disposed++; } };
  putFixture(context, fixture);
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  return { registry, context, fixture, invoke, evidenceStore, get reads() { return reads; }, get disposed() { return disposed; }, setPayload(value: Json) { payload = value; } };
}
const input = { agent: { fixtureLeaseId: 'lease', workspaceId: 'workspace', agentId: 'agt_owned' }, after: 0, pageSize: 2, maxPages: 2, maxRecords: 10, kinds: [] };
test('public registry validates before transport and returns redacted typed evidence', async t => {
  const harness = await setup(t);
  await assert.rejects(harness.invoke('loom.api.savedEvents', { ...input, command: 'unsafe' }));
  assert.equal(harness.reads, 0);
  const observed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(observed.availability, 'observed'); assert.equal(observed.provenance.evidenceClass, 'deterministic');
  assert.equal(observed.provenance.identity.agentId, 'agt_owned');
  assert.ok(!JSON.stringify(observed).includes('private-password'));
  assert.equal(observed.provenance.artifacts[0]!.redaction, 'sanitized');
  const retained = await harness.evidenceStore.resolve(observed.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), observed.data);
  const schema = harness.registry.get('loom.api.savedEvents', 1).outputSchema;
  const parsed = schema.parse(observed.data);
  assert.equal(parsed.events[0].payload.text, 'independently observed value');
  assert.notEqual(parsed.events[0].payload.text, 'caller expected value');
  harness.setPayload({ text: 'changed actual value' });
  const changed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(schema.parse(changed.data).events[0].payload.text, 'changed actual value');
});
test('foreign, missing and expired leases never read observations; cleanup survives abort', async t => {
  const harness = await setup(t);
  const foreign = await harness.invoke('loom.api.savedEvents', { ...input, agent: { ...input.agent, fixtureLeaseId: 'foreign' } });
  assert.equal(foreign.availability, 'error'); assert.equal(foreign.data, undefined); assert.equal(harness.reads, 0);
  const other = { ...harness.context, caseId: 'case_2' };
  await assert.rejects(getFixture(other, 'lease'));
  harness.fixture.expiresAtUtcMs = 0;
  assert.equal((await harness.invoke('loom.api.savedEvents', input)).availability, 'error');
  const abort = new AbortController(); abort.abort(); harness.context.signal = abort.signal;
  await disposeFixtures(harness.context); assert.equal(harness.disposed, 1);
  await disposeFixtures(harness.context); assert.equal(harness.disposed, 1);
});
test('incomplete and unreadable events cannot become empty successful evidence', async t => {
  const harness = await setup(t);
  harness.fixture.readApi = async () => ({ status: 200, body: { events: [], snapshot_seq: 1, next: 0, more: false } });
  const incomplete = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(incomplete.availability, 'incomplete'); assert.equal(incomplete.data, undefined);
  harness.fixture.readApi = async () => ({ status: 403, body: { password: 'private-password' } });
  const failed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(failed.availability, 'error'); assert.equal(failed.data, undefined);
  assert.ok(!JSON.stringify(failed).includes('private-password'));
});

test('two child cases use only exported suite fixture handles; case cleanup cannot release them', async t => {
  const harness = await setup(t);
  const suiteContext = { ...harness.context, scope: 'suite' as const, resources: new Map<string, unknown>() };
  putEvidenceStore(suiteContext, harness.evidenceStore);
  const suiteFixture = { ...harness.fixture, scope: 'suite' as const, leaseId: 'suite-lease' };
  putFixture(suiteContext, suiteFixture);
  const child = (caseId: string, handles: string[]) => ({ ...harness.context, caseId, resources: new Map<string, unknown>(),
    suite: { id: suiteContext.suiteId, handles, getResource(key: string, handle: string) {
      assert.equal(handle, 'suite-lease'); assert.ok(handles.includes(handle)); return suiteContext.resources.get(key);
    } } });
  const one = child('one', ['suite-lease']); const two = child('two', ['suite-lease']);
  assert.equal(await getFixture(one, 'suite-lease'), suiteFixture);
  assert.equal(await getFixture(two, 'suite-lease'), suiteFixture);
  await assert.rejects(getFixture(child('undeclared', []), 'suite-lease'));
  await assert.rejects(getFixture({ ...one, suiteId: 'foreign-suite' }, 'suite-lease'));
  const suiteInput = { ...input, agent: { ...input.agent, fixtureLeaseId: 'suite-lease' } };
  const observed = await harness.registry.invoke({ id: 'loom.api.savedEvents', version: 1, input: {} }, suiteInput, one);
  assert.equal(observed.availability, 'observed');
  assert.ok(await harness.evidenceStore.resolve(observed.provenance.artifacts[0]!.id));
  await disposeFixtures(one); await disposeFixtures(two); assert.equal(harness.disposed, 0);
  suiteFixture.expiresAtUtcMs = 0;
  await disposeFixtures(suiteContext); assert.equal(harness.disposed, 1);
  assert.equal(suiteContext.resources.has('@loom/aft-adapter/fixtures/v1:suite-lease'), false);
});

test('exact event selector rejects foreign turn/request/item and missing identifiers through registry', async t => {
  const harness = await setup(t);
  harness.setPayload({ itemId: 'item_1', requestId: 'request_1', text: 'actual answer' });
  const correlated = { ...input, eventIds: ['event_1'], turnId: 'turn_1', itemId: 'item_1', requestId: 'request_1' };
  assert.equal((await harness.invoke('loom.api.correlate', correlated)).availability, 'observed');
  for (const corrupt of [{ turnId: 'foreign' }, { itemId: 'foreign' }, { requestId: 'foreign' }, { eventIds: ['missing'] }, { eventIds: ['event_1', 'event_1'] }]) {
    const result = await harness.invoke('loom.api.correlate', { ...correlated, ...corrupt });
    assert.equal(result.availability, 'error'); assert.equal(result.data, undefined);
  }
});

test('native model and deletion evidence rejects foreign/stale/duplicate proofs through the public registry', async t => {
  const harness = await setup(t); const agent = harness.fixture.agents.get('agt_owned')!;
  const row = agent.row;
  let status = 200;
  let body: Json = { data: { id: 'ses_owned', metadata: { agent_id: row.agent_id }, location: { directory: row.worktree_path } } };
  let messages: Json = { data: [{ id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: { completed: 1 }, finish: 'stop',
    model: { providerID: 'provider', id: 'actual-model' } }] };
  agent.native = { pinnedExecutable: '/owned/opencode',
    registration: async () => ({ url: 'http://127.0.0.1:4123/', password: 'private-password', pid: 42, generation: 'gen_1', endpointId: 'endpoint_1' }),
    process: async () => ({ pid: 42, generation: 'gen_1', executable: '/owned/opencode', argv: ['/owned/opencode', 'serve', '--service'] }),
    sessions: async () => [{ agent_id: row.agent_id, harness: 'opencode', native_root: '', native_id: 'ses_owned' }], agent: async () => row,
    read: async route => route === '/api/info' ? { status: 200, body: { pid: 42 } } : route.includes('/message?') ? { status: 200, body: messages } : { status, body } };
  const nativeInput = { agent: input.agent, view: 'completed-models', nativeSessionId: 'ses_owned', nativeRoot: '', expectedGeneration: 'gen_1', maxMessages: 200 };
  const schema = harness.registry.get('loom.native.observe', 1).outputSchema;
  const models = await harness.invoke('loom.native.observe', nativeInput);
  assert.equal(models.availability, 'observed');
  assert.equal(schema.parse(models.data).records[0].model, 'actual-model');
  assert.notEqual(schema.parse(models.data).records[0].model, 'expected-model');
  const stale = await harness.invoke('loom.native.observe', { ...nativeInput, expectedGeneration: 'stale' });
  assert.equal(stale.availability, 'error'); assert.equal(stale.data, undefined);
  messages = { data: [{ id: 'msg_1', sessionID: 'foreign', type: 'assistant' }] };
  assert.equal((await harness.invoke('loom.native.observe', nativeInput)).availability, 'error');
  messages = { data: [{ id: 'msg_1', sessionID: 'ses_owned' }, { id: 'msg_1', sessionID: 'ses_owned' }] };
  assert.equal((await harness.invoke('loom.native.observe', nativeInput)).availability, 'error');
  status = 404; body = { _tag: 'SessionNotFoundError', sessionID: 'ses_owned', message: 'Session not found: ses_owned' };
  const absent = await harness.invoke('loom.native.observe', { ...nativeInput, view: 'presence' });
  assert.equal(absent.availability, 'observed'); assert.equal(schema.parse(absent.data).present, false);
  body = { _tag: 'NotFoundError' };
  const unknown = await harness.invoke('loom.native.observe', { ...nativeInput, view: 'presence' });
  assert.equal(unknown.availability, 'error'); assert.equal(unknown.data, undefined);
});

test('failed cleanup retains the exact owned fixture for a final retry after expiry and abort', async t => {
  const harness = await setup(t); let attempts = 0;
  harness.fixture.dispose = async () => { if (++attempts === 1) throw new Error('deterministic cleanup failure'); };
  harness.fixture.expiresAtUtcMs = 0; const abort = new AbortController(); abort.abort(); harness.context.signal = abort.signal;
  await assert.rejects(disposeFixtures(harness.context));
  assert.equal(harness.context.resources.get('@loom/aft-adapter/fixtures/v1:lease'), harness.fixture);
  await disposeFixtures(harness.context); await disposeFixtures(harness.context);
  assert.equal(attempts, 2);
});
