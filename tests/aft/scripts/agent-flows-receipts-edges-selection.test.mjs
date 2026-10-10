import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const [loader, authoredSuites] = process.argv.slice(2);
assert.ok(loader && authoredSuites, 'pass the built AFT runner and authored suite directory');
const root = mkdtempSync('/private/tmp/aft-receipts-edges-selection-');
const coverage = join(root, 'live-agent-coverage-suites');
const original = join(root, 'live-agent-flow-suites');
const catalogPath = join(root, 'agent-flow-batches.json');
const suitePath = join(coverage, 'receipts-stream-edges.test.yaml');
const selector = join(testsDir, 'scripts/agent-flows-selection.mjs');
const ownership = join(testsDir, 'scripts/agent-flows-ownership.sh');
const env = { ...process.env, AFT_BASE_URL: 'http://127.0.0.1:1', AFT_API_URL: 'http://127.0.0.1:1',
  AFT_WS: 'LOCALMODE', AFT_TESTS_DIR: root, AFT_WORK_DIR: root, RUN_ID: 'validation',
  AFT_REAL_MODEL: 'openai/gpt-5.5', AFT_REAL_BACKEND: 'opencode',
  AFT_AGENT_FLOW_REPO: '/workspace/source-repo',
  AFT_SELECT_AGENT_MODEL: '/private/tmp/select-model',
  AFT_NATIVE_MODEL_PROBE: '/private/tmp/native-model',
  AFT_NATIVE_SESSION_PROBE: '/private/tmp/native-session' };
const run = batch => JSON.parse(execFileSync(process.execPath, [selector, root, loader, batch],
  { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }));

try {
  mkdirSync(coverage);
  mkdirSync(original);
  cpSync(join(testsDir, 'agent-flow-batches.json'), catalogPath);
  cpSync(join(authoredSuites, 'receipts-stream-edges.test.yaml'), suitePath);
  for (const name of ['children', 'lead-chat', 'recovery-lifecycle'])
    cpSync(join(testsDir, 'live-agent-flow-suites', `${name}.test.yaml`), join(original, `${name}.test.yaml`));

  const selected = run('receipts-edges');
  const suiteSource = readFileSync(suitePath, 'utf8');
  assert.equal(selected.count, 3);
  assert.deepEqual(selected.cases, [
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE 1.5d interrupt with message cancels running turn and delivers replacement once' },
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE 1.6c 1.7b queued UTF-8 body limit and rejected over-cap receipt' },
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE CR1 2.0n rejected Create inputs leave no Agent or worktree then same-name retry succeeds' }]);
  assert.equal(selected.suites.length, 1);
  assert.equal(selected.suites[0].sha256, createHash('sha256').update(suiteSource).digest('hex'));
  assert.deepEqual(selected.agents, {
    leads: [
      { name: 'coverage-rs-edges-interrupt-${RUN_ID}', suite: 'live-receipts-stream-edges', model_required: true },
      { name: 'coverage-rs-edges-create-${RUN_ID}', suite: 'live-receipts-stream-edges',
        model_required: true, model_proof: 'api_post_create' }],
    reviewers: [{ name: 'coverage-rs-edges-large-${RUN_ID}', suite: 'live-receipts-stream-edges' }],
    children: [] });
  assert.equal(run('default').count, 9);

  const refuseSuite = changed => {
    writeFileSync(suitePath, changed);
    try { assert.throws(() => run('receipts-edges')); }
    finally { writeFileSync(suitePath, suiteSource); }
  };
  refuseSuite(suiteSource.replace('delivers replacement once', 'claims unproved replay order'));
  refuseSuite(suiteSource.replace('suite: live-receipts-stream-edges', 'suite: foreign-suite'));
  refuseSuite(suiteSource.replace('tests:\n',
    'tests:\n  - name: extra paid case\n    steps:\n      - open: /ws/${AFT_WS}/agents\n'));

  const catalogSource = readFileSync(catalogPath, 'utf8');
  const refuseCatalog = change => {
    const data = JSON.parse(catalogSource);
    change(data.batches['receipts-edges']);
    writeFileSync(catalogPath, JSON.stringify(data));
    try { assert.throws(() => run('receipts-edges')); }
    finally { writeFileSync(catalogPath, catalogSource); }
  };
  refuseCatalog(batch => { batch.files = ['foreign.test.yaml']; });
  refuseCatalog(batch => { batch.expected_cases.pop(); });
  refuseCatalog(batch => { batch.agents.leads[0].name = 'foreign-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.leads[0].model_required = false; });
  refuseCatalog(batch => { batch.agents.leads[1].model_proof = 'ui_selection'; });
  refuseCatalog(batch => { batch.agents.reviewers[0].name = 'foreign-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.children.push({ name: 'foreign-${RUN_ID}',
    parent: 'coverage-rs-edges-interrupt-${RUN_ID}', suite: 'live-receipts-stream-edges' }); });
  rmSync(suitePath);
  symlinkSync(join(original, 'lead-chat.test.yaml'), suitePath);
  assert.throws(() => run('receipts-edges'));
  rmSync(suitePath);

  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: selected }));
  const expanded = JSON.parse(execFileSync('bash',
    ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded.leads.map(lead => [lead.name, lead.model_required, lead.model_proof]), [
    ['coverage-rs-edges-interrupt-af12345678', true, 'ui_selection'],
    ['coverage-rs-edges-create-af12345678', true, 'api_post_create']]);
  assert.deepEqual(expanded.reviewers,
    [{ name: 'coverage-rs-edges-large-af12345678', suite: 'live-receipts-stream-edges' }]);
  assert.deepEqual(expanded.children, []);
  console.log('receipts edges selection: authored three cases, exact ownership, and unsafe substitutions passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
