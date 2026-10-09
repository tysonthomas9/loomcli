import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, writeFile, realpath, rm, rename, symlink, unlink } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { DatabaseSync } from 'node:sqlite';
import { createNativeHostAccess } from './native-host.js';
import { createEvidenceStore } from './evidence.js';
import { projectNativeLog } from './native-failure.js';
import { observeGit, type GitReader } from './git.js';
import { sha256 } from './protocol.js';

async function directory(t: { after(fn: () => Promise<void>): void }) {
  const root = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-adapter-contract-')));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}
const frame = (data: unknown) => `data: ${JSON.stringify(data)}\n\n`;
const durable = (seq: number, type: string, data: unknown = {}) => ({ id: `evt_${seq}`, type, data,
  durable: { aggregateID: 'ses_owned', seq, version: 1 } });
const first = durable(0, 'session.created', { sessionID: 'ses_owned' });
const failure = durable(1, 'session.execution.failed', { sessionID: 'ses_owned', error: { type: 'provider.auth', message: 'private-password', status: 401 } });
const log = (rows: unknown[], seq = rows.length - 1) => rows.map(frame).join('') + frame({ type: 'log.synced', aggregateID: 'ses_owned', seq });

test('native durable failures retain actual hashes and counts independently of expected results', async () => {
  const result = await projectNativeLog(log([first, failure]), 'ses_owned', 10);
  assert.deepEqual(result.eventIds, ['evt_0', 'evt_1']);
  assert.equal(result.failures[0]!.messageSha256, await sha256('private-password'));
  assert.equal(result.failures[0]!.messageBytes, 16);
  assert.ok(!JSON.stringify(result).includes('private-password'));
  const extra = durable(2, 'session.execution.failed', { error: { type: 'tool.execution', message: '' } });
  assert.equal((await projectNativeLog(log([first, failure, extra]), 'ses_owned', 10)).failures.length, 2);
  assert.equal((await projectNativeLog(log([first]), 'ses_owned', 10)).failures.length, 0);
});
for (const [name, raw] of Object.entries({
  foreign: log([first, { ...failure, durable: { aggregateID: 'ses_foreign', seq: 1 } }]),
  foreignData: log([first, { ...failure, data: { sessionID: 'ses_foreign', error: { type: 'provider.auth', message: 'x' } } }]),
  duplicate: log([first, { ...failure, id: 'evt_0' }]),
  missing: log([first, { ...failure, durable: { aggregateID: 'ses_owned', seq: 2 } }]),
  stale: log([first, { ...failure, durable: { aggregateID: 'ses_owned', seq: 0 } }]),
  watermark: log([first, failure], 3),
  unsynced: [first, failure].map(frame).join(''),
  truncated: log([first, failure]).slice(0, -1),
  wrongPrefix: log([durable(0, 'session.updated')]),
  afterWatermark: log([first]) + frame(failure),
  badCategory: log([first, durable(1, 'session.execution.failed', { error: { type: 'invented', message: 'x' } })]),
})) test(`native durable log rejects ${name} evidence`, async () => {
  await assert.rejects(projectNativeLog(raw, 'ses_owned', 10));
});
test('native logs reject bounds, malformed frames and duplicate watermarks', async () => {
  await assert.rejects(projectNativeLog(log([first, failure]), 'ses_owned', 1));
  await assert.rejects(projectNativeLog(log([first]) + frame({ type: 'log.synced', aggregateID: 'ses_owned', seq: 0 }), 'ses_owned', 10));
  await assert.rejects(projectNativeLog('data: not-json\n\n', 'ses_owned', 10));
});

test('retained evidence resolves exact serialized bytes and rejects tampering and foreign references', async t => {
  const root = await directory(t); const store = await createEvidenceStore(root);
  const receipt = await store.retain('{"safe":true}');
  assert.equal(receipt.bytes, 13); assert.equal(receipt.sha256, await sha256('{"safe":true}'));
  const file = await store.resolve(receipt.id);
  await assert.rejects(store.resolve('../foreign'));
  await writeFile(file, '{"safe":false}');
  await assert.rejects(store.resolve(receipt.id));
  await assert.rejects(store.retain('{"safe":true}'));
  await unlink(file); await symlink('/etc/hosts', file);
  await assert.rejects(store.resolve(receipt.id));
});
test('evidence root replacement is rejected', async t => {
  const root = await directory(t); const store = await createEvidenceStore(root);
  await store.retain('{}');
  await rename(root, root + '-old'); t.after(() => rm(root + '-old', { recursive: true }));
  await mkdir(root);
  await assert.rejects(store.retain('{}'));
});

test('native host reads owned SQLite and fixed authenticated routes with deterministic process/fetch ports', async t => {
  const root = await directory(t);
  await mkdir(path.join(root, 'agents-opencode/state/opencode'), { recursive: true });
  const registration = path.join(root, 'agents-opencode/state/opencode/service.json');
  await writeFile(registration, JSON.stringify({ pid: 42, url: 'http://127.0.0.1:4123/', password: 'private-password', ignored: true }));
  const db = new DatabaseSync(path.join(root, 'agents.db'));
  db.exec(`CREATE TABLE agents (agent_id TEXT, workspace_id TEXT, repo TEXT, worktree_path TEXT, branch TEXT, harness TEXT,
    harness_session_id TEXT, harness_session_root TEXT, parent_agent_id TEXT, root_agent_id TEXT, created_by_kind TEXT, created_by_id TEXT,
    preset TEXT, revision INTEGER, state TEXT, running_turn_id TEXT, deleted_at TEXT, history_purged_at TEXT);
    CREATE TABLE agent_native_sessions (agent_id TEXT, harness TEXT, native_root TEXT, native_id TEXT);`);
  db.prepare('INSERT INTO agents VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run('agt_owned', 'workspace', '/owned/source', '/owned/tree',
    'loom/agent/owned', 'opencode', 'ses_owned', '', null, null, 'user', null, 'lead', 1, 'idle', null, null, null);
  db.prepare('INSERT INTO agent_native_sessions VALUES (?,?,?,?)').run('agt_owned', 'opencode', '', 'ses_owned'); db.close();
  const calls: string[] = [];
  const access = createNativeHostAccess({ configRoot: root, workspaceId: 'workspace', repo: '/owned/source', pinnedExecutable: '/owned/opencode',
    processIdentity: async pid => ({ pid, generation: 'generation', executable: '/owned/opencode', argv: ['/owned/opencode', 'serve', '--service'] }),
    fetch: async (target, options) => {
      const url = String(target); calls.push(url);
      assert.equal((options!.headers as Record<string, string>).Authorization, `Basic ${Buffer.from('opencode:private-password').toString('base64')}`);
      assert.equal(options!.redirect, 'error');
      return url.includes('/log?') ? new Response(log([first]), { headers: { 'content-type': 'text/event-stream' } }) :
        new Response('{"pid":42}', { headers: { 'content-type': 'application/json' } });
    } });
  assert.equal((await access.agent('agt_owned')).harness_session_root, '');
  assert.equal((await access.sessions('agt_owned'))[0]!.native_id, 'ses_owned');
  const privateLease = await access.registration(); assert.equal(privateLease.generation, 'generation');
  assert.equal(privateLease.password, 'private-password');
  assert.equal((await access.read('/api/info', new AbortController().signal)).status, 200);
  assert.equal(await access.log!('ses_owned', new AbortController().signal), log([first]));
  for (const route of ['/api/workspaces/foreign', '/api/session/..', '/api/session/%2E%2E', '/api/session/%2fsecret', 'https://foreign/'])
    await assert.rejects(access.read(route, new AbortController().signal));
  assert.equal(calls.length, 2);
  await assert.rejects(access.agent('foreign'));
  await unlink(registration); await symlink('/etc/hosts', registration);
  await assert.rejects(access.registration());
});

test('Git observations reject foreign identity, changed HEAD, duplicates and failed reads', async t => {
  const root = await directory(t); const common = path.join(root, 'git'); await mkdir(common);
  const input = { agent: { fixtureLeaseId: 'lease', workspaceId: 'workspace', agentId: 'agt_owned' }, view: 'status' as const, paths: [], maxBytes: 10000 };
  let mode = 'normal'; let heads = 0;
  const reader: GitReader = { async read(args) {
    if (mode === 'error') return { code: 2, stdout: '' };
    let stdout = '';
    if (args[0] === 'rev-parse') {
      if (args.includes('--show-toplevel')) stdout = mode === 'foreign' ? '/foreign' : root;
      else if (args.includes('--git-common-dir')) stdout = common;
      else stdout = mode === 'changed' && ++heads > 1 ? 'b'.repeat(40) : 'a'.repeat(40);
    } else if (args[0] === 'branch') stdout = 'owned';
    else if (args[0] === 'status') stdout = mode === 'duplicate' ? ' M file\0 M file\0' : ' M file\0';
    else if (args[0] === 'for-each-ref') stdout = `refs/heads/owned\t${'a'.repeat(40)}\n`;
    return { code: 0, stdout };
  } };
  assert.equal((await observeGit(input, { worktree: root, commonDir: common, branch: 'owned' }, reader)).status[0]!.path, 'file');
  for (mode of ['foreign', 'changed', 'duplicate', 'error']) {
    heads = 0; await assert.rejects(observeGit(input, { worktree: root, commonDir: common, branch: 'owned' }, reader));
  }
});
