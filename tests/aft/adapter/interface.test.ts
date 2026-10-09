import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { readdir } from 'node:fs/promises';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin } from '@tysonthomas9/aft/capabilities';
import { createCoreProviders } from './index.js';
import { putFixture, getFixture, disposeFixtures, type OwnedFixture } from './ownership.js';
import { AgentRow, type Json } from './protocol.js';

async function setup() {
  const root = fileURLToPath(new URL('.', import.meta.url));
  const files = (await readdir(root)).filter(name => name.endsWith('.ts') && !name.endsWith('.test.ts'));
  const pin = calculateImplementationPin(root, files, 'index.ts', 'createCoreProviders');
  const registry = new CapabilityRegistry();
  for (const provider of createCoreProviders(pin)) registry.register(provider);
  const context = createCapabilityContext({ file: 'deterministic.test.yaml', line: 1 }, registry, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002');
  const row = AgentRow.parse({ agent_id: 'agt_owned', workspace_id: 'workspace', repo: '/owned/source', worktree_path: '/owned/tree',
    branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '', parent_agent_id: null,
    root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead', revision: 1, state: 'idle', running_turn_id: null,
    deleted_at: null, history_purged_at: null });
  let disposed = 0;
  let reads = 0;
  let payload: Json = { text: 'independently observed value', password: 'private-password' };
  const fixture: OwnedFixture = { leaseId: 'lease', runId: context.runId, caseId: context.caseId, workspaceId: 'workspace', repo: '/owned/source',
    profile: 'deterministic', expiresAtUtcMs: Date.now() + 100000, evidenceClass: 'deterministic', roots: new Map(),
    agents: new Map([['agt_owned', { row, commonDir: '/owned/source/.git' }]]), secrets: ['private-password'],
    readApi: async () => { reads++; return { status: 200, body: { events: [{ agent_id: row.agent_id, seq: 1, event_id: 'event_1',
      kind: 'item.completed', turn_id: 'turn_1', payload, created_at: '2026-10-09T00:00:00Z' }], snapshot_seq: 1, next: 1, more: false } }; },
    readFiles: async () => ({ status: 404, body: {} }), resolveAgent: async () => ({ row, commonDir: '/owned/source/.git' }),
    verify: async () => {}, dispose: async () => { disposed++; } };
  putFixture(context, fixture);
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  return { registry, context, fixture, invoke, get reads() { return reads; }, get disposed() { return disposed; }, setPayload(value: Json) { payload = value; } };
}
const input = { agent: { fixtureLeaseId: 'lease', workspaceId: 'workspace', agentId: 'agt_owned' }, after: 0, pageSize: 2, maxPages: 2, maxRecords: 10, kinds: [] };
test('public registry validates before transport and returns redacted typed evidence', async () => {
  const harness = await setup();
  await assert.rejects(harness.invoke('loom.api.savedEvents', { ...input, command: 'unsafe' }));
  assert.equal(harness.reads, 0);
  const observed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(observed.availability, 'observed'); assert.equal(observed.provenance.evidenceClass, 'deterministic');
  assert.equal(observed.provenance.identity.agentId, 'agt_owned');
  assert.ok(!JSON.stringify(observed).includes('private-password'));
  assert.equal(observed.provenance.artifacts[0]!.redaction, 'sanitized');
  const schema = harness.registry.get('loom.api.savedEvents', 1).outputSchema;
  const parsed = schema.parse(observed.data);
  assert.equal(parsed.events[0].payload.text, 'independently observed value');
  assert.notEqual(parsed.events[0].payload.text, 'caller expected value');
  harness.setPayload({ text: 'changed actual value' });
  const changed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(schema.parse(changed.data).events[0].payload.text, 'changed actual value');
});
test('foreign, missing and expired leases never read observations; cleanup survives abort', async () => {
  const harness = await setup();
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
test('incomplete and unreadable events cannot become empty successful evidence', async () => {
  const harness = await setup();
  harness.fixture.readApi = async () => ({ status: 200, body: { events: [], snapshot_seq: 1, next: 0, more: false } });
  const incomplete = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(incomplete.availability, 'incomplete'); assert.equal(incomplete.data, undefined);
  harness.fixture.readApi = async () => ({ status: 403, body: { password: 'private-password' } });
  const failed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(failed.availability, 'error'); assert.equal(failed.data, undefined);
  assert.ok(!JSON.stringify(failed).includes('private-password'));
});
