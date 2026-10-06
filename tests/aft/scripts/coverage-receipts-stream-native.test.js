// Offline checks for the exact embedded native-history probe; no stack is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const shell = fs.readFileSync(path.join(__dirname, 'coverage-receipts-stream-native.sh'), 'utf8');
const match = shell.match(/node -e '\n([\s\S]*?)\n' "\$agent_id"/);
assert.ok(match, 'embedded native probe is present');
const probe = match[1];
const inputKey = `msg_${'a'.repeat(26)}`;

test('native probe uses direct podman exec arguments, never Compose-only -T', () => {
  const invocation = text => text.match(/^result="\$\(agent_flows_podman (exec[^\n]*?) node -e '/m)?.[1];
  const supported = text => invocation(text) === 'exec "$container"';
  assert.equal(supported(shell), true);
  assert.equal(supported(shell.replace('exec "$container"', 'exec -T "$container"')), false);
  assert.match(shell, /agent_flows_check_manifest[\s\S]*agent_flows_check_container "\$container"[\s\S]*agent_flows_podman exec "\$container" node -e/);
});

async function run(registration, messagePages = [[{type: 'user', id: inputKey}]], native = {}) {
  let stdout = '';
  let stderr = '';
  let fetches = 0;
  const paths = [];
  const row = {agent_id: 'agt_1', name: 'coverage-rs-create-validation', harness: 'opencode',
    worktree_path: '/root/.loom/worktrees/source-repo/agt_1', native_id: 'ses_1', native_root: ''};
  const fakeFs = {
    readdirSync(target) {
      assert.equal(target, '/proc');
      return ['321'];
    },
    readFileSync(target) {
      if (target === '/proc/321/cmdline') return Buffer.from('/usr/bin/opencode\0serve\0--service\0');
      assert.equal(target, '/root/.loom/agents-opencode/state/opencode/service.json');
      return JSON.stringify(registration);
    },
  };
  class DatabaseSync {
    constructor(filename, options) {
      assert.equal(filename, '/root/.loom/agents.db');
      assert.equal(options.readOnly, true);
    }
    prepare(query) {
      return {get: () => query.includes('FROM agent_native_sessions') ? {owned: 1} : row};
    }
  }
  const fakeProcess = {
    argv: ['node', 'agt_1', 'validation', 'count', inputKey],
    env: {}, // podman exec does not inherit the entrypoint's LOOM_CONFIG_DIR.
    stdout: {write: value => { stdout += value; }},
    stderr: {write: value => { stderr += value; }},
    exit: code => { throw new Error(`probe exit ${code}`); },
  };
  const context = {require: name => name === 'node:fs' ? fakeFs :
    name === 'node:sqlite' ? {DatabaseSync} : assert.fail(`unexpected module ${name}`),
    process: fakeProcess, Buffer, URL, URLSearchParams, AbortSignal,
    fetch: async url => {
      assert.equal(url.hostname, '127.0.0.1');
      paths.push(url.pathname);
      if (url.pathname === '/api/info')
        return {ok: true, json: async () => native.info ?? {pid: 321}};
      if (url.pathname === '/api/session/ses_1')
        return {ok: true, json: async () => native.session ?? {data: {id: 'ses_1',
          metadata: {agent_id: 'agt_1'}, location: {directory: row.worktree_path}}}};
      assert.equal(url.pathname, '/api/session/ses_1/message');
      assert.equal(url.searchParams.get('limit'), '200');
      if (fetches === 0) {
        assert.equal(url.searchParams.get('order'), 'asc');
        assert.equal(url.searchParams.has('cursor'), false);
      } else {
        assert.equal(url.searchParams.get('cursor'), 'next-page');
        assert.equal(url.searchParams.has('order'), false);
      }
      const index = fetches++;
      assert.ok(index < messagePages.length, 'unexpected extra native page');
      return {ok: true, json: async () => ({data: messagePages[index],
        cursor: index + 1 < messagePages.length ? {next: 'next-page'} : {}})};
    }};
  let failure;
  try {
    await vm.runInNewContext(probe, context);
  } catch (error) {
    failure = error;
  }
  return {stdout, stderr, fetches, paths, failure};
}

test('native count reads the fixed owned registry without inherited config env', async () => {
  const result = await run({pid: 321, url: 'http://127.0.0.1:49999/', password: 'offline-secret'});
  assert.equal(result.failure, undefined);
  assert.equal(result.fetches, 1);
  assert.deepEqual(result.paths, ['/api/info', '/api/session/ses_1', '/api/session/ses_1/message']);
  const receipt = JSON.parse(result.stdout);
  assert.equal(receipt.native_user_message_count, 1);
  assert.equal(receipt.input_key, inputKey);
  assert.equal(result.stdout.includes('offline-secret'), false);
});

test('native count rejects a foreign host before fetching history', async () => {
  const result = await run({pid: 321, url: 'http://169.254.169.254:49999/', password: 'offline-secret'});
  assert.match(result.stderr, /service-registration-mismatch/);
  assert.equal(result.fetches, 0);
});

test('native count rejects a foreign service PID before fetching history', async () => {
  const result = await run({pid: 999, url: 'http://127.0.0.1:49999/', password: 'offline-secret'});
  assert.match(result.stderr, /service-registration-mismatch/);
  assert.equal(result.fetches, 0);
});

test('native count rejects a live service PID mismatch before history', async () => {
  const result = await run({pid: 321, url: 'http://127.0.0.1:49999/', password: 'offline-secret'},
    undefined, {info: {pid: 999}});
  assert.match(result.stderr, /native-session-mismatch/);
  assert.equal(result.fetches, 0);
});

test('native count rejects a foreign session owner before history', async () => {
  const result = await run({pid: 321, url: 'http://127.0.0.1:49999/', password: 'offline-secret'},
    undefined, {session: {data: {id: 'ses_1', metadata: {agent_id: 'agt_other'},
      location: {directory: '/root/.loom/worktrees/source-repo/agt_1'}}}});
  assert.match(result.stderr, /native-session-mismatch/);
  assert.equal(result.fetches, 0);
});

test('native count follows cursor without order and finds input on page two', async () => {
  const first = Array.from({length: 200}, (_, index) => ({type: 'assistant', id: `other_${index}`}));
  const result = await run({pid: 321, url: 'http://127.0.0.1:49999/', password: 'offline-secret'},
    [first, [{type: 'user', id: inputKey}]]);
  assert.equal(result.failure, undefined);
  assert.equal(result.fetches, 2);
  assert.equal(JSON.parse(result.stdout).native_user_message_count, 1);
});

test('native count rejects a missing user input', async () => {
  const result = await run({pid: 321, url: 'http://127.0.0.1:49999/', password: 'offline-secret'},
    [[{type: 'assistant', id: 'other'}]]);
  assert.match(result.stderr, /native-input-count-mismatch/);
  assert.equal(result.fetches, 1);
});
