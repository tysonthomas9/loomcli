import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { dirname, join } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const script = join(dirname(fileURLToPath(import.meta.url)), 'agent-flows-deleted-proof.mjs');
const root = mkdtempSync('/private/tmp/aft-deleted-proof-');
const run = 'af12345678';
const suite = 'coverage-lifecycle-delete';
const session = `aft-${suite}-0`;
const target = 'openai/gpt-5.5';
const repo = '/workspace/source-repo';
const base = { workspace_id: 'LOCALMODE', repo, harness: 'opencode', model: target,
  model_unverified: false, state: 'idle', deleted_at: null };
const lead = { ...base, agent_id: 'agt_lead', name: `cov-delete-parent-${run}`, preset: 'lead',
  created_by_kind: 'user', parent_agent_id: null, root_agent_id: null };
const child = { ...base, agent_id: 'agt_child', name: `cov-delete-child-${run}`, preset: 'task',
  created_by_kind: 'agent', parent_agent_id: lead.agent_id, root_agent_id: lead.agent_id, model: null };
const declared = { leads: [{ name: lead.name, suite, model_required: true, end_state: 'deleted' }],
  children: [{ name: child.name, parent: lead.name, suite, end_state: 'deleted' }] };
const natives = [lead, child].map(row => ({ agent_id: row.agent_id, harness: 'opencode',
  native_id: `ses_${row.agent_id}`, native_root: '' }));
const receipts = [lead, child].map((row, index) => ({ run_id: run, suite, session,
  captured_at: '2026-10-06T20:00:00Z', api: row, native: natives[index] }));
const selections = [{ run_id: run, session, agent_id: lead.agent_id, name: lead.name,
  time: '2026-10-06T19:59:00Z', ui_selected_model: target, observed_saved_model: target }];
let replies = Object.fromEntries([lead, child].map(row => [row.agent_id,
  { ...row, state: 'deleted', deleted_at: '2026-10-06T20:01:00Z' }]));
const server = createServer((request, response) => {
  const id = request.url?.split('/').at(-1);
  const row = replies[id];
  response.writeHead(row === 'unauthorized' ? 401 : row ? 200 : 404, { 'content-type': 'application/json' });
  response.end(JSON.stringify(row ?? { error: 'not found' }));
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const port = server.address().port;
const write = (name, rows) => writeFileSync(join(root, name),
  name === 'actual-agent-models.json' ? JSON.stringify(rows) : rows.map(row => JSON.stringify(row)).join('\n') + '\n');
const check = async (nextReceipts = receipts, nextNatives = natives, nextSelections = selections,
  nextSurvivors = []) => {
  write('lifecycle-delete-preflight.jsonl', nextReceipts);
  write('native-sessions.jsonl', nextNatives);
  write('model-selections.jsonl', nextSelections);
  write('actual-agent-models.json', nextSurvivors);
  return promisify(execFile)(process.execPath, [script, root, JSON.stringify(declared), run,
    `http://127.0.0.1:${port}`, repo, target]);
};
const refuse = async (...args) => assert.rejects(check(...args));
try {
  const good = JSON.parse((await check()).stdout);
  assert.deepEqual(good.map(row => row.agent_id), [lead.agent_id, child.agent_id]);
  await refuse(receipts.map((row, i) => i ? row : { ...row, run_id: 'foreign' }));
  await refuse(receipts.map((row, i) => i ? row : { ...row, session: 'aft-foreign-0' }));
  await refuse(receipts.map((row, i) => i ? row : { ...row, api: { ...row.api, model: 'openai/foreign' } }));
  await refuse(receipts.map((row, i) => i ? row : { ...row, api: { ...row.api, agent_id: 'agt_foreign' } }));
  await refuse(receipts.map((row, i) => i ? row : { ...row, native: { ...row.native, native_id: 'ses_foreign' } }));
  await refuse(receipts, natives, []);
  await refuse(receipts, natives, [{ ...selections[0], agent_id: 'agt_foreign' }]);
  await refuse(receipts, natives, [{ ...selections[0], time: undefined }]);
  await refuse(receipts, natives, selections, [{ ...lead, agent_id: 'agt_foreign' }]);
  await refuse(receipts.map((row, i) => i ? { ...row, api: { ...row.api, parent_agent_id: 'agt_foreign' } } : row));
  replies = { ...replies, [lead.agent_id]: { ...lead, state: 'idle' } };
  await refuse();
  replies = { ...replies, [lead.agent_id]: { ...lead, state: 'deleted', deleted_at: '2026-10-06T20:01:00Z', workspace_id: 'FOREIGN' } };
  await refuse();
  replies = { ...replies, [lead.agent_id]: null };
  await refuse();
  replies = { ...replies, [lead.agent_id]: 'unauthorized' };
  await refuse();
  replies = { ...replies, [lead.agent_id]: { ...lead, state: 'deleted', deleted_at: '2026-10-06T20:01:00Z', name: 'foreign' } };
  await refuse();
  console.log('deleted Agent proof: exact API tombstone, ownership, model, native and negative checks passed');
} finally {
  server.close();
  rmSync(root, { recursive: true, force: true });
}
