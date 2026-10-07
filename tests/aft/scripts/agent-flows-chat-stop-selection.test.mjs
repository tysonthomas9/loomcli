import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const loader = process.argv[2];
assert.ok(loader, 'pass the built AFT runner');
const root = mkdtempSync('/private/tmp/aft-chat-stop-selection-');
const coverage = join(root, 'live-agent-coverage-suites');
const original = join(root, 'live-agent-flow-suites');
const catalogPath = join(root, 'agent-flow-batches.json');
const suitePath = join(coverage, 'chat-controls-stop.test.yaml');
const selector = join(testsDir, 'scripts/agent-flows-selection.mjs');
const ownership = join(testsDir, 'scripts/agent-flows-ownership.sh');
const env = { ...process.env, AFT_BASE_URL: 'http://127.0.0.1:1', AFT_API_URL: 'http://127.0.0.1:1',
  AFT_WS: 'LOCALMODE', AFT_TESTS_DIR: root, AFT_WORK_DIR: root, RUN_ID: 'validation',
  AFT_REAL_MODEL: 'openai/gpt-5.5', AFT_REAL_BACKEND: 'opencode',
  AFT_AGENT_FLOW_REPO: '/workspace/source-repo' };
const run = batch => JSON.parse(execFileSync(process.execPath, [selector, root, loader, batch],
  { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }));

try {
  mkdirSync(coverage);
  mkdirSync(original);
  cpSync(join(testsDir, 'agent-flow-batches.json'), catalogPath);
  cpSync(join(testsDir, 'live-agent-coverage-suites/chat-controls-stop.test.yaml'), suitePath);
  for (const name of ['children', 'lead-chat', 'recovery-lifecycle'])
    cpSync(join(testsDir, 'live-agent-flow-suites', `${name}.test.yaml`), join(original, `${name}.test.yaml`));

  const selected = run('chat-stop');
  const suiteSource = readFileSync(suitePath, 'utf8');
  assert.equal(selected.count, 1);
  assert.deepEqual(selected.cases, [{ suite: 'live-chat-controls-stop',
    name: 'OC1 bare Chat Stop loses the native ask without executing it and permits a real next turn' }]);
  assert.equal(selected.suites.length, 1);
  assert.equal(selected.suites[0].sha256, createHash('sha256').update(suiteSource).digest('hex'));
  assert.deepEqual(selected.agents, { leads: [],
    reviewers: [{ name: 'cov-controls-stop-${RUN_ID}', suite: 'live-chat-controls-stop' }], children: [] });
  assert.equal(run('default').count, 9);

  const refuseSuite = changed => {
    writeFileSync(suitePath, changed);
    try { assert.throws(() => run('chat-stop')); }
    finally { writeFileSync(suitePath, suiteSource); }
  };
  refuseSuite(suiteSource.replace('OC1 bare Chat Stop', 'foreign case'));
  refuseSuite(suiteSource.replace('suite: live-chat-controls-stop', 'suite: foreign-suite'));
  refuseSuite(suiteSource.replace('tests:\n',
    'tests:\n  - name: extra paid case\n    steps:\n      - open: /ws/${AFT_WS}/agents\n'));

  const catalogSource = readFileSync(catalogPath, 'utf8');
  const refuseCatalog = change => {
    const data = JSON.parse(catalogSource);
    change(data.batches['chat-stop']);
    writeFileSync(catalogPath, JSON.stringify(data));
    try { assert.throws(() => run('chat-stop')); }
    finally { writeFileSync(catalogPath, catalogSource); }
  };
  refuseCatalog(batch => { batch.files = ['chat-controls-models.test.yaml']; });
  refuseCatalog(batch => { batch.expected_cases[0].name = 'foreign case'; });
  refuseCatalog(batch => { batch.agents.reviewers[0].name = 'foreign-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.reviewers[0].suite = 'live-chat-controls-models'; });
  refuseCatalog(batch => { batch.agents.leads.push({ name: 'foreign-${RUN_ID}',
    suite: 'live-chat-controls-stop', model_required: false }); });
  refuseCatalog(batch => { batch.agents.children.push({ name: 'foreign-${RUN_ID}',
    parent: 'cov-controls-stop-${RUN_ID}', suite: 'live-chat-controls-stop' }); });
  rmSync(suitePath);
  symlinkSync(join(original, 'lead-chat.test.yaml'), suitePath);
  assert.throws(() => run('chat-stop'));
  rmSync(suitePath);

  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: selected }));
  const expanded = JSON.parse(execFileSync('bash',
    ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded.leads, []);
  assert.deepEqual(expanded.reviewers,
    [{ name: 'cov-controls-stop-af12345678', suite: 'live-chat-controls-stop' }]);
  assert.deepEqual(expanded.children, []);
  console.log('chat Stop selection: authored one case, exact reviewer, and unsafe substitutions passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
