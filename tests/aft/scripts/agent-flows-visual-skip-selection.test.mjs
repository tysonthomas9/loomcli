import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const [loader, candidate] = process.argv.slice(2);
assert.ok(loader && candidate, 'pass the built AFT runner and authored coverage suite directory');
const root = mkdtempSync('/private/tmp/aft-visual-skip-selection-');
const coverage = join(root, 'live-agent-coverage-suites');
const original = join(root, 'live-agent-flow-suites');
const catalogPath = join(root, 'agent-flow-batches.json');
const suitePath = join(coverage, 'chat-visual-skip.test.yaml');
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
  cpSync(join(candidate, 'chat-visual-skip.test.yaml'), suitePath);
  for (const name of ['children', 'lead-chat', 'recovery-lifecycle'])
    cpSync(join(testsDir, 'live-agent-flow-suites', `${name}.test.yaml`), join(original, `${name}.test.yaml`));
  const selected = run('chat-visual-skip');
  assert.equal(selected.count, 1);
  assert.deepEqual(selected.cases, [{ suite: 'live-chat-visual-skip',
    name: 'normal-motion skip link stays hidden after mouse focus' }]);
  assert.deepEqual(selected.agents, { leads: [{ name: 'cov-visual-skip-${RUN_ID}',
    suite: 'live-chat-visual-skip', model_required: false }], children: [] });
  assert.equal(run('default').count, 9);

  const suite = readFileSync(suitePath, 'utf8');
  const refuseSuite = changed => {
    writeFileSync(suitePath, changed);
    assert.throws(() => run('chat-visual-skip'));
    writeFileSync(suitePath, suite);
  };
  refuseSuite(suite.replace('normal-motion skip link stays hidden after mouse focus', 'different case'));
  refuseSuite(suite.replace('tests:\n',
    'tests:\n  - name: extra case\n    steps:\n      - open: /ws/${AFT_WS}/agents\n'));
  refuseSuite(suite.replace('suite: live-chat-visual-skip', 'suite: foreign-suite'));
  refuseSuite(suite.replace('cov-visual-skip-${RUN_ID}', 'foreign-lead-${RUN_ID}'));
  refuseSuite(suite.replace('      - reload: true',
    '      - fill: { label: Message, value: unintended model request }\n      - reload: true'));

  const catalog = readFileSync(catalogPath, 'utf8');
  const refuseCatalog = change => {
    const data = JSON.parse(catalog);
    change(data.batches['chat-visual-skip']);
    writeFileSync(catalogPath, JSON.stringify(data));
    assert.throws(() => run('chat-visual-skip'));
    writeFileSync(catalogPath, catalog);
  };
  refuseCatalog(batch => { batch.agents.leads[0].name = 'foreign-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.leads[0].model_required = true; });
  refuseCatalog(batch => { batch.files = ['chat-visual.test.yaml']; });
  rmSync(suitePath);
  symlinkSync(join(original, 'lead-chat.test.yaml'), suitePath);
  assert.throws(() => run('chat-visual-skip'));
  rmSync(suitePath);
  writeFileSync(suitePath, suite);

  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: selected }));
  const expanded = JSON.parse(execFileSync('bash',
    ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded, { leads: [{ name: 'cov-visual-skip-af12345678',
    suite: 'live-chat-visual-skip', model_required: false, model_exception: false,
    model_proof: 'ui_selection' }], reviewers: [], children: [] });
  console.log('visual skip selection: exact no-answer case, default nine, and unsafe substitutions passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
