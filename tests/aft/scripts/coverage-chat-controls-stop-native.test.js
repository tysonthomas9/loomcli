// Offline refusal checks for the native model probe's credentialed-read boundary.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const shell = fs.readFileSync(path.join(__dirname, 'coverage-chat-controls-stop-native.sh'), 'utf8');
const program = shell.match(/node -e '\n([\s\S]*?)\n' "\$agent_id"/);
assert.ok(program, 'native probe program is present');

function refusal(registration, command) {
  const output = [];
  let credentialedReads = 0;
  const row = {agent_id: 'agt_a', name: 'cov-controls-stop-oracle',
    repo: '/workspace/source-repo', harness: 'opencode', preset: 'pr-review-interactive',
    model: 'openai/gpt', native_id: 'ses_a', native_root: '',
    worktree_path: '/workspace/source-repo'};
  const fakeFs = {
    readFileSync(file) {
      if (file.endsWith('/service.json')) return JSON.stringify(registration);
      if (file === '/proc/123/cmdline') return command;
      throw Error('foreign file');
    },
    realpathSync(file) { return file === '/proc/123/exe' ? '/usr/local/bin/opencode' : file; },
  };
  class DatabaseSync {
    prepare(sql) { return {get: () => sql.includes('FROM agent_native_sessions') ? {owned: 1} : row}; }
  }
  vm.runInNewContext(program[1], {
    require(name) {
      if (name === 'node:fs') return fakeFs;
      if (name === 'node:path') return path;
      if (name === 'node:sqlite') return {DatabaseSync};
      throw Error('foreign module');
    },
    process: {argv: ['node', 'agt_a', 'oracle', 'openai/gpt', '/workspace/source-repo'],
      env: {LOOM_CONFIG_DIR: '/root/.loom', LOOM_OPENCODE_BIN: '/usr/local/bin/opencode'},
      stdout: {write: value => output.push(value)}},
    URL,
    Buffer,
    fetch: () => { credentialedReads++; throw Error('credentialed GET reached'); },
  });
  assert.equal(output.length, 1, 'probe must report one refusal');
  assert.equal(credentialedReads, 0, 'probe must refuse before the credentialed GET');
  return JSON.parse(output[0]);
}

const owned = {url: 'http://127.0.0.1:31337/', pid: 123, password: 'private-test-value'};

test('foreign registration origin is refused before a credentialed native GET', () => {
  for (const url of ['http://foreign.example:31337/', 'http://127.0.0.1/',
    'http://127.0.0.1:31337/?foreign=1', 'http://127.0.0.1:31337/#foreign']) {
    assert.equal(refusal({...owned, url}, '/usr/local/bin/opencode\0serve\0--service\0').reason,
      'service-identity-invalid');
  }
});

test('foreign process at registered PID is refused before a credentialed native GET', () => {
  assert.equal(refusal(owned, '/usr/local/bin/other\0serve\0--service\0').reason,
    'service-process-not-pinned-opencode');
});
