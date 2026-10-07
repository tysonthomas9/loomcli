import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const [loader, authoredSuites] = process.argv.slice(2);
assert.ok(loader && authoredSuites, 'pass the built AFT runner and authored suite directory');
const root = mkdtempSync('/private/tmp/aft-lifecycle-selection-');
const coverage = join(root, 'live-agent-coverage-suites');
const original = join(root, 'live-agent-flow-suites');
const catalogPath = join(root, 'agent-flow-batches.json');
const suitePath = join(coverage, 'lifecycle-delete.test.yaml');
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
  cpSync(join(authoredSuites, 'lifecycle-delete.test.yaml'), suitePath);
  for (const name of ['children', 'lead-chat', 'recovery-lifecycle'])
    cpSync(join(testsDir, 'live-agent-flow-suites', `${name}.test.yaml`), join(original, `${name}.test.yaml`));

  const selected = run('lifecycle-delete');
  assert.equal(selected.count, 2);
  assert.deepEqual(selected.cases, [
    { suite: 'coverage-lifecycle-delete', name: 'stale-delete' },
    { suite: 'coverage-lifecycle-delete', name: 'native-cascade' }]);
  assert.deepEqual(selected.agents, {
    leads: [
      { name: 'cov-delete-control-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true },
      { name: 'cov-delete-target-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true,
        end_state: 'deleted' },
      { name: 'cov-delete-parent-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true,
        end_state: 'deleted' }],
    children: [{ name: 'cov-delete-child-${RUN_ID}', parent: 'cov-delete-parent-${RUN_ID}',
      suite: 'coverage-lifecycle-delete', end_state: 'deleted' }] });
  assert.equal(run('default').count, 9);

  const suiteSource = readFileSync(suitePath, 'utf8');
  const refuseSuite = changed => {
    writeFileSync(suitePath, changed);
    assert.throws(() => run('lifecycle-delete'));
    writeFileSync(suitePath, suiteSource);
  };
  refuseSuite(suiteSource.replace('stale-delete', 'foreign-case'));
  refuseSuite(suiteSource.replace('suite: coverage-lifecycle-delete', 'suite: foreign-suite'));
  refuseSuite(suiteSource.replace('tests:\n',
    'tests:\n  - name: extra paid case\n    steps:\n      - open: /ws/${AFT_WS}/agents\n'));

  const catalogSource = readFileSync(catalogPath, 'utf8');
  const refuseCatalog = change => {
    const data = JSON.parse(catalogSource);
    change(data.batches['lifecycle-delete']);
    writeFileSync(catalogPath, JSON.stringify(data));
    assert.throws(() => run('lifecycle-delete'));
    writeFileSync(catalogPath, catalogSource);
  };
  refuseCatalog(batch => { batch.files = ['foreign.test.yaml']; });
  refuseCatalog(batch => { batch.agents.leads[1].name = 'foreign-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.leads[1].model_required = false; });
  refuseCatalog(batch => { batch.agents.leads[1].end_state = 'present'; });
  refuseCatalog(batch => { batch.agents.children[0].parent = 'cov-delete-control-${RUN_ID}'; });
  refuseCatalog(batch => { batch.agents.children[0].end_state = 'present'; });
  rmSync(suitePath);
  symlinkSync(join(original, 'lead-chat.test.yaml'), suitePath);
  assert.throws(() => run('lifecycle-delete'));
  rmSync(suitePath);

  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: selected }));
  const expanded = JSON.parse(execFileSync('bash',
    ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded.leads.map(lead => [lead.name, lead.model_required, lead.end_state]), [
    ['cov-delete-control-af12345678', true, 'present'],
    ['cov-delete-target-af12345678', true, 'deleted'],
    ['cov-delete-parent-af12345678', true, 'deleted']]);
  assert.deepEqual(expanded.children, [{ name: 'cov-delete-child-af12345678',
    parent: 'cov-delete-parent-af12345678', suite: 'coverage-lifecycle-delete', end_state: 'deleted' }]);
  console.log('lifecycle selection: authored two cases, exact ownership, and unsafe substitutions passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
