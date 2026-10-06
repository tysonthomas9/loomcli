import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const selector = join(testsDir, 'scripts/agent-flows-selection.mjs');
const ownership = join(testsDir, 'scripts/agent-flows-ownership.sh');
const loader = process.argv[2];
assert.ok(loader, 'pass the built testing-app dist/runner.js path');
const root = mkdtempSync('/private/tmp/aft-batches-');
const original = join(root, 'live-agent-flow-suites');
const coverage = join(root, 'live-agent-coverage-suites');
mkdirSync(original);
mkdirSync(coverage);
for (const file of ['children', 'lead-chat', 'recovery-lifecycle'])
  cpSync(join(testsDir, 'live-agent-flow-suites', `${file}.test.yaml`), join(original, `${file}.test.yaml`));
const catalog = join(root, 'agent-flow-batches.json');
const agents = { leads: [{ name: 'aft-coverage-${RUN_ID}', suite: 'coverage-one' }],
  children: [{ name: 'aft-child-${RUN_ID}', parent: 'aft-coverage-${RUN_ID}', suite: 'coverage-one' }] };
const write = files => writeFileSync(catalog, JSON.stringify({ version: 1, batches: { smoke: { files, agents } } }));
const env = { ...process.env, AFT_BASE_URL: 'http://127.0.0.1:1', AFT_API_URL: 'http://127.0.0.1:1',
  AFT_WS: 'LOCALMODE', AFT_TESTS_DIR: testsDir, AFT_WORK_DIR: root, RUN_ID: 'validation',
  AFT_REAL_MODEL: 'openai/gpt-5.5', AFT_REAL_BACKEND: 'opencode',
  AFT_AGENT_FLOW_REPO: '/workspace/source-repo',
  AFT_SELECT_AGENT_MODEL: '/private/tmp/select-model',
  AFT_NATIVE_MODEL_PROBE: '/private/tmp/native-model',
  AFT_NATIVE_SESSION_PROBE: '/private/tmp/native-session',
  AFT_RESTART_SERVE: '/private/tmp/restart' };
const run = batch => execFileSync(process.execPath, [selector, root, loader, batch], { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const refuse = batch => assert.throws(() => run(batch));
try {
  write(['one.test.yaml']);
  const originalSource = readFileSync(join(original, 'lead-chat.test.yaml'), 'utf8');
  writeFileSync(join(coverage, 'one.test.yaml'), originalSource.replace('suite: live-lead-chat', 'suite: coverage-one'));
  const selected = JSON.parse(run('smoke'));
  assert.equal(selected.count, 3);
  assert.deepEqual(selected.cases.map(test => test.suite), Array(3).fill('coverage-one'));
  assert.match(selected.suites[0].sha256, /^[a-f0-9]{64}$/);
  assert.equal(JSON.parse(run('default')).count, 9);
  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: { batch: 'smoke', agents } }));
  const expanded = JSON.parse(execFileSync('bash', ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, AFT_WORK_DIR: root, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded, { leads: [{ name: 'aft-coverage-af12345678', suite: 'coverage-one' }],
    children: [{ name: 'aft-child-af12345678', parent: 'aft-coverage-af12345678', suite: 'coverage-one' }] });
  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: { batch: 'default', agents: null } }));
  assert.equal(execFileSync('bash', ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, AFT_WORK_DIR: root, RUN_ID: 'af12345678' }, encoding: 'utf8' }).trim(), 'null');
  refuse('unknown');
  write([]);
  refuse('smoke');
  write(['one.test.yaml', 'one.test.yaml']);
  refuse('smoke');
  write(['../lead-chat.test.yaml']);
  refuse('smoke');
  write(['one.test.yaml']);
  rmSync(join(coverage, 'one.test.yaml'));
  symlinkSync(join(original, 'lead-chat.test.yaml'), join(coverage, 'one.test.yaml'));
  refuse('smoke');
  rmSync(join(coverage, 'one.test.yaml'));
  const files = [];
  for (let i = 0; i < 4; i++) {
    const file = `batch-${i}.test.yaml`;
    writeFileSync(join(coverage, file), originalSource.replace('suite: live-lead-chat', `suite: coverage-${i}`));
    files.push(file);
  }
  write(files);
  refuse('smoke'); // Twelve parsed cases exceed the absolute ten-case ceiling.
  agents.leads[0].suite = 'foreign-suite';
  write(['one.test.yaml']);
  writeFileSync(join(coverage, 'one.test.yaml'), originalSource.replace('suite: live-lead-chat', 'suite: coverage-one'));
  refuse('smoke');
  agents.leads[0].suite = 'coverage-one';
  agents.children[0].parent = 'aft-foreign-${RUN_ID}';
  write(['one.test.yaml']);
  refuse('smoke');
  console.log('agent-flow selection: default, named batch, and unsafe selections passed');
} finally {
  rmSync(root, { recursive: true, force: true });
}
