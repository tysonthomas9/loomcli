import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { chmodSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const testsDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = resolve(testsDir, '../..');
const suffix = randomBytes(4).toString('hex');
const run = `af${suffix}`;
const root = `/private/tmp/aft-agent-flows.${suffix}`;
const evidence = join(root, 'evidence');
const bin = join(root, 'bin');
const home = join(root, 'host-home');
const profiles = join(root, 'profiles');
for (const path of [evidence, bin, home, profiles]) mkdirSync(path, { recursive: true });
const script = join(testsDir, 'scripts/agent-flows-select-model.sh');
const agentFile = join(root, 'agent.json');
const connection = '[{"Name":"mock","Default":true,"URI":"unix:///mock","Identity":""}]';
const fingerprint = createHash('sha256').update('{"Identity":"","Name":"mock","URI":"unix:///mock"}').digest('hex');
const head = execFileSync('git', ['-C', source, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
const ui = 'http://127.0.0.1:5678';
const api = 'http://127.0.0.1:1234';
const name = `aft-coverage-${run}`;
const goodAgent = { agent_id: 'agt_ABC', name, harness: 'opencode',
  repo: '/root/.loom/workspaces/LOCALMODE/source-repo', preset: 'lead',
  created_by_kind: 'user', parent_agent_id: null, root_agent_id: null,
  model: 'openai/gpt-5.5', model_unverified: false };
const manifest = { run_id: run, source_root: source, source_head: head, backend: 'opencode',
  owned: { compose_project: `loom-aft-agents-${run}`, evidence_dir: evidence,
    api_url: api, ui_url: ui, ports: [1111, 1234, 5678], podman_home: home,
    podman_connection: 'mock', podman_connection_fingerprint: fingerprint },
  fixture_repo: { seed_path: '/workspace/source-repo', managed_path: goodAgent.repo },
  required_ui_model: goodAgent.model,
  selection: { batch: 'coverage', agents: {
    leads: [{ name: 'aft-coverage-${RUN_ID}', suite: 'coverage-one' }], children: [] },
    suites: [{ name: 'coverage-one' }] } };
const mock = (name, body) => {
  const path = join(bin, name);
  writeFileSync(path, `#!/usr/bin/env bash\n${body}\n`);
  chmodSync(path, 0o755);
};
mock('podman', `printf '%s\\n' '${connection}'`);
mock('agent-browser', 'printf "%s\\n" "$MOCK_CHAT_URL"');
mock('curl', 'cat "$MOCK_AGENT_JSON"');
writeFileSync(join(evidence, 'manifest.json'), JSON.stringify(manifest));
writeFileSync(join(evidence, 'model-selection.json'), JSON.stringify({ target: goodAgent.model,
  displayed_default: goodAgent.model, alternate: 'openai/other', harness: 'opencode' }));
const baseEnv = { ...process.env, PATH: `${bin}:${process.env.PATH}`, RUN_ID: run,
  AFT_OWNED_PROJECT: manifest.owned.compose_project, AFT_SOURCE_ROOT: source,
  AFT_WORK_DIR: evidence, AFT_API_URL: api, AFT_BASE_URL: ui,
  AFT_PODMAN_HOME: home, AFT_PODMAN_CONNECTION: 'mock',
  AFT_AGENT_FLOW_REPO: goodAgent.repo, AFT_REAL_MODEL: goodAgent.model,
  AFT_SESSION: 'aft-coverage-one-123', AFT_BROWSER_PROFILES: profiles,
  MOCK_CHAT_URL: `${ui}/ws/LOCALMODE/chat/agt_ABC`, MOCK_AGENT_JSON: agentFile };
const probe = (agent = goodAgent, env = {}) => {
  writeFileSync(agentFile, JSON.stringify(agent));
  return spawnSync('bash', [script], { env: { ...baseEnv, ...env }, encoding: 'utf8' });
};
try {
  assert.equal(probe().status, 0, 'declared Lead on its own Chat session must pass');
  assert.notEqual(probe(goodAgent, { AFT_SESSION: 'aft-foreign-123' }).status, 0);
  assert.notEqual(probe({ ...goodAgent, name: `aft-foreign-${run}` }).status, 0);
  assert.notEqual(probe({ ...goodAgent, repo: '/foreign/repo' }).status, 0);
  assert.notEqual(probe({ ...goodAgent, harness: 'claude' }).status, 0);
  assert.notEqual(probe({ ...goodAgent, parent_agent_id: 'agt_FOREIGN' }).status, 0);
  assert.notEqual(probe({ ...goodAgent, root_agent_id: 'agt_FOREIGN' }).status, 0);
  assert.notEqual(probe(goodAgent, { MOCK_CHAT_URL: `${ui}/ws/LOCALMODE/chat/agt_FOREIGN` }).status, 0);
  console.log('agent-flow ownership: declared Lead accepted; foreign session/name/repo/backend/root/parent/route refused');
} finally {
  rmSync(root, { recursive: true, force: true });
}
