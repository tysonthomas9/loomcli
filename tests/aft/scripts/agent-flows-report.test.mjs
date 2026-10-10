import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const runner = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'run-aft-agent-flows.sh'), 'utf8');
const extract = (start, end) => {
  const a = runner.indexOf(start);
  assert.ok(a >= 0, `missing runner predicate start: ${start}`);
  const b = runner.indexOf(end, a + start.length);
  assert.ok(b > a, `missing runner predicate end: ${end}`);
  return runner.slice(a + start.length, b);
};
const identity = extract('  jq -e --arg repo "$AFT_AGENT_FLOW_REPO" --arg target "$real_model" --argjson declared "$declared" \'\n',
  "' \\\n    \"$run_root/evidence/actual-agent-models.json\"");
const catalogCheck = extract('  jq -e --slurpfile observed "$run_root/evidence/actual-agent-models.json" --argjson declared "$declared" \'\n',
  "' \\\n    <<< \"$catalog\"");
const receipts = extract('      --argjson declared "$declared" --arg target "$real_model" --arg run "$run_id" \'\n',
  "' \\\n      \"$run_root/evidence/model-selections.jsonl\"");
const root = mkdtempSync('/private/tmp/aft-report-');
const observedFile = join(root, 'observed.json');
const declared = { leads: [
  { name: 'target', suite: 'coverage-one', model_required: true, model_exception: false, model_proof: 'ui_selection' },
  { name: 'created', suite: 'coverage-create', model_required: true, model_exception: false, model_proof: 'api_post_create' },
  { name: 'ui', suite: 'coverage-one', model_required: false, model_exception: false, model_proof: 'ui_selection' },
  { name: 'negative', suite: 'coverage-one', model_required: false, model_exception: true, model_proof: 'ui_selection' }],
  reviewers: [{ name: 'reviewer' }], children: [{ name: 'child', parent: 'target' }] };
const base = { repo: '/owned', harness: 'opencode', model_unverified: false, state: 'idle' };
const observed = [
  { ...base, agent_id: 'agt_T', name: 'target', preset: 'lead', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null, model: 'openai/gpt-5.5' },
  { ...base, agent_id: 'agt_A', name: 'created', preset: 'lead', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null, model: 'openai/gpt-5.5' },
  { ...base, agent_id: 'agt_U', name: 'ui', preset: 'lead', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null, model: null },
  { ...base, agent_id: 'agt_N', name: 'negative', preset: 'lead', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null, model: 'openai/unknown', model_unverified: true },
  { ...base, agent_id: 'agt_R', name: 'reviewer', preset: 'pr-review-interactive', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null, model: 'openai/gpt-5.5' },
  { ...base, agent_id: 'agt_C', name: 'child', preset: 'task', created_by_kind: 'agent', parent_agent_id: 'agt_T', root_agent_id: 'agt_T', model: null }];
const runJq = (filter, args, input) => spawnSync('jq', [...args, filter], { input: JSON.stringify(input), encoding: 'utf8' });
const args = ['-e', '--arg', 'repo', '/owned', '--arg', 'target', 'openai/gpt-5.5', '--argjson', 'declared', JSON.stringify(declared)];
try {
  assert.equal(runJq(identity, args, observed).status, 0);
  assert.notEqual(runJq(identity, args, observed.map(a => a.name === 'child' ? { ...a, parent_agent_id: 'agt_U', root_agent_id: 'agt_U' } : a)).status, 0);
  assert.notEqual(runJq(identity, args, observed.map(a => a.name === 'reviewer' ? { ...a, name: 'foreign' } : a)).status, 0);
  assert.notEqual(runJq(identity, args, observed.map(a => a.name === 'reviewer' ? { ...a, model: null } : a)).status, 0);
  assert.notEqual(runJq(identity, args, observed.map(a => a.name === 'target' ? { ...a, model: null } : a)).status, 0);
  assert.notEqual(runJq(identity, args, [...observed, { ...observed[0], agent_id: 'agt_DUP' }]).status, 0, 'duplicate observed names refused');
  assert.notEqual(runJq(identity, args, [...observed, { ...observed[0], name: 'foreign' }]).status, 0, 'duplicate observed IDs refused');
  const models = { providers: [{ models: [{ id: 'openai/gpt-5.5' }] }] };
  writeFileSync(observedFile, JSON.stringify(observed));
  const catalogArgs = ['-e', '--slurpfile', 'observed', observedFile, '--argjson', 'declared', JSON.stringify(declared)];
  assert.equal(runJq(catalogCheck, catalogArgs, models).status, 0, 'declared negative model may be absent from catalog');
  writeFileSync(observedFile, JSON.stringify(observed.map(a => a.name === 'ui' ? { ...a, model: 'openai/unknown' } : a)));
  assert.notEqual(runJq(catalogCheck, catalogArgs, models).status, 0, 'ordinary UI Lead cannot bypass catalog');
  writeFileSync(observedFile, JSON.stringify(observed));
  const receiptArgs = ['-se', '--slurpfile', 'observed', observedFile, '--argjson', 'declared', JSON.stringify(declared),
    '--arg', 'target', 'openai/gpt-5.5', '--arg', 'run', 'af12345678'];
  const receipt = { run_id: 'af12345678', session: 'aft-coverage-one-0', agent_id: 'agt_T', name: 'target',
    ui_selected_model: 'openai/gpt-5.5', observed_saved_model: 'openai/gpt-5.5' };
  const created = { run_id: 'af12345678', session: 'aft-coverage-create-0', agent_id: 'agt_A', name: 'created',
    initial_saved_model: 'openai/gpt-5.5', ui_selected_model: null, observed_saved_model: 'openai/gpt-5.5' };
  const checkReceipts = rows => spawnSync('jq', [...receiptArgs, receipts],
    { input: rows.map(row => JSON.stringify(row)).join('\n') + '\n', encoding: 'utf8' }).status;
  assert.equal(checkReceipts([receipt, created]), 0, 'exact UI and post-Create receipts accepted');
  assert.notEqual(checkReceipts([{ ...receipt, agent_id: 'agt_U' }, created]), 0, 'name alone cannot satisfy observed ID');
  assert.notEqual(checkReceipts([receipt, { ...created, initial_saved_model: null }]), 0, 'post-Create needs actual initial target');
  assert.notEqual(checkReceipts([receipt, { ...created, session: 'aft-coverage-one-0' }]), 0, 'wrong suite browser session refused');
  assert.notEqual(checkReceipts([receipt, created, receipt]), 0, 'duplicate receipt ID refused');
  console.log('agent-flow report: exact reviewer/child identity and model exception/receipt gates passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
