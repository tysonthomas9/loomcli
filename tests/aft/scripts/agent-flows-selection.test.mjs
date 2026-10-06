import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
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
const agents = { leads: [{ name: 'aft-coverage-${RUN_ID}', suite: 'coverage-one', model_required: true }],
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
  const writeExpected = expected_cases => writeFileSync(catalog, JSON.stringify({ version: 1,
    batches: { smoke: { files: ['one.test.yaml'], agents, expected_cases } } }));
  writeExpected(selected.cases);
  assert.equal(JSON.parse(run('smoke')).count, 3);
  writeExpected(selected.cases.slice(0, 2));
  refuse('smoke'); // Extra selected case cannot enter an exact reviewed batch.
  writeExpected([selected.cases[1], selected.cases[0], selected.cases[2]]);
  refuse('smoke');
  writeExpected([selected.cases[0], selected.cases[0]]);
  refuse('smoke');
  writeFileSync(catalog, JSON.stringify({ version: 1, batches: { 'tool-policy':
    { files: ['one.test.yaml'], agents } } }));
  refuse('tool-policy'); // This batch must always pin its three reachable cases.
  cpSync(join(coverage, 'one.test.yaml'), join(coverage, 'tool-policy.test.yaml'));
  writeFileSync(catalog, JSON.stringify({ version: 1, batches: { 'tool-policy':
    { files: ['tool-policy.test.yaml'], agents, expected_cases: [...selected.cases,
      { suite: 'coverage-one', name: 'unexpected fourth case' }] } } }));
  refuse('tool-policy');
  write(['one.test.yaml']);
  assert.equal(JSON.parse(run('default')).count, 9);
  writeFileSync(join(original, 'lead-chat.test.yaml'), originalSource.replace('tests:\n',
    'tests:\n  - name: unexpected tenth default case\n    steps:\n      - open: /ws/${AFT_WS}/agents\n'));
  refuse('default'); // Even cap ten cannot expand the original three-by-three selection.
  writeFileSync(join(original, 'lead-chat.test.yaml'), originalSource);
  agents.leads[0].model_required = false;
  write(['one.test.yaml']);
  assert.equal(JSON.parse(run('smoke')).count, 3);
  agents.leads[0].model_required = true;
  agents.leads[0].model_proof = 'api_post_create';
  write(['one.test.yaml']);
  assert.equal(JSON.parse(run('smoke')).agents.leads[0].model_proof, 'api_post_create');
  delete agents.leads[0].model_proof;
  agents.leads[0].model_exception = true;
  write(['one.test.yaml']);
  refuse('smoke');
  delete agents.leads[0].model_exception;
  agents.reviewers = [{ name: 'aft-review-${RUN_ID}', suite: 'coverage-one' }];
  write(['one.test.yaml']);
  assert.equal(JSON.parse(run('smoke')).agents.reviewers.length, 1);
  delete agents.reviewers;
  writeFileSync(join(root, 'manifest.json'), JSON.stringify({ selection: { batch: 'smoke', agents } }));
  const expanded = JSON.parse(execFileSync('bash', ['-c', 'source "$1"; agent_flows_declared_agents', 'bash', ownership],
    { env: { ...env, AFT_WORK_DIR: root, RUN_ID: 'af12345678' }, encoding: 'utf8' }));
  assert.deepEqual(expanded, { leads: [{ name: 'aft-coverage-af12345678', suite: 'coverage-one', model_required: true, model_exception: false, model_proof: 'ui_selection' }],
    reviewers: [],
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

const policyCandidate = process.argv[3];
if (policyCandidate) {
  const policyRoot = mkdtempSync('/private/tmp/aft-policy-candidate-');
  try {
    const suites = join(policyRoot, 'live-agent-coverage-suites');
    mkdirSync(suites);
    cpSync(join(testsDir, 'agent-flow-batches.json'), join(policyRoot, 'agent-flow-batches.json'));
    for (const file of ['tool-policy.test.yaml', 'tool-policy-denial.test.yaml'])
      cpSync(join(policyCandidate, file), join(suites, file));
    const candidateRun = () => execFileSync(process.execPath, [selector, policyRoot, loader, 'tool-policy'],
      { env: { ...env, AFT_TESTS_DIR: policyRoot, AFT_WORK_DIR: policyRoot }, encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'] });
    const selected = JSON.parse(candidateRun());
    const reviewed = JSON.parse(readFileSync(join(testsDir, 'agent-flow-batches.json'), 'utf8')).batches['tool-policy'];
    assert.equal(selected.count, 3);
    assert.deepEqual(selected.cases, reviewed.expected_cases);
    assert.deepEqual(selected.suites.map(suite => basename(suite.path)), ['tool-policy.test.yaml']);
    assert.equal(selected.agents.reviewers, undefined);
    cpSync(join(suites, 'tool-policy-denial.test.yaml'), join(suites, 'tool-policy.test.yaml'));
    assert.throws(candidateRun, 'the denied reviewer suite cannot replace paid policy selection');
    console.log('agent-flow policy: exact authored three cases selected; reviewer denial refused');
  } finally {
    rmSync(policyRoot, { recursive: true, force: true });
  }
}
