import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, writeFile, symlink, realpath, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { collectSavedEvents, SavedEventsInput } from './events.js';
import { observeNative, NativeInput } from './native.js';
import { observeFilesystem, FilesystemInput } from './filesystem.js';
import { observeFiles } from './files.js';
import { AgentRow, sha256, type NativeAccess, type Json } from './protocol.js';
const signal = new AbortController().signal;
const ref = { fixtureLeaseId: 'lease', workspaceId: 'workspace', agentId: 'agt_owned' };
const row = AgentRow.parse({ agent_id: ref.agentId, workspace_id: ref.workspaceId, repo: '/owned/source', worktree_path: '/owned/tree',
  branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '', parent_agent_id: null,
  root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead', revision: 1, state: 'idle', running_turn_id: null,
  deleted_at: null, history_purged_at: null });
const event = (seq: number) => ({ agent_id: ref.agentId, seq, event_id: `event_${seq}`, kind: 'item.completed', turn_id: 'turn_1',
  payload: { itemId: `item_${seq}`, text: `answer ${seq}` }, created_at: '2026-10-09T00:00:00Z' });
const eventsInput = SavedEventsInput.parse({ agent: ref, after: 0, pageSize: 2, maxPages: 3, maxRecords: 10, kinds: [] });
test('saved event pages pin snapshot and retain exact identity/order', async () => {
  const calls: string[] = [];
  const observed = await collectSavedEvents(eventsInput, async route => {
    calls.push(route); return { status: 200, body: calls.length === 1 ? { events: [event(1), event(2)], snapshot_seq: 3, next: 2, more: true } :
      { events: [event(3)], snapshot_seq: 3, next: 3, more: false } };
  }, signal);
  assert.deepEqual(observed.events.map(e => e.eventId), ['event_1', 'event_2', 'event_3']); assert.match(calls[1]!, /snapshot=3/);
});
for (const [name, page] of Object.entries({
  foreign: { events: [{ ...event(1), agent_id: 'foreign' }], snapshot_seq: 1, next: 1, more: false },
  duplicate: { events: [event(1), { ...event(2), event_id: 'event_1' }], snapshot_seq: 2, next: 2, more: false },
  stale: { events: [event(1), event(1)], snapshot_seq: 2, next: 1, more: false },
  missing: { events: [event(2)], snapshot_seq: 2, next: 2, more: false },
  tail: { events: [event(1)], snapshot_seq: 2, next: 1, more: false },
  cursor: { events: [event(1)], snapshot_seq: 1, next: 2, more: false },
  stalled: { events: [], snapshot_seq: 1, next: 0, more: true },
})) test(`saved events reject ${name} evidence`, async () => {
  await assert.rejects(collectSavedEvents(eventsInput, async () => ({ status: 200, body: page }), signal));
});
test('saved events reject changing snapshots, unreadable data and page overflow', async () => {
  let call = 0;
  await assert.rejects(collectSavedEvents(eventsInput, async () => ({ status: 200, body: ++call === 1 ?
    { events: [event(1)], snapshot_seq: 2, next: 1, more: true } : { events: [event(2)], snapshot_seq: 3, next: 2, more: false } }), signal));
  await assert.rejects(collectSavedEvents(eventsInput, async () => ({ status: 404, body: {} }), signal));
  await assert.rejects(collectSavedEvents({ ...eventsInput, maxPages: 1 }, async () => ({ status: 200, body:
    { events: [event(1)], snapshot_seq: 2, next: 1, more: true } }), signal));
});
function access(session: Json, status = 200): NativeAccess {
  return { pinnedExecutable: '/usr/local/bin/opencode',
    registration: async () => ({ url: 'http://127.0.0.1:4123/', password: 'private-password', pid: 42, generation: 'gen_1', endpointId: 'endpoint_1' }),
    process: async () => ({ pid: 42, generation: 'gen_1', executable: '/usr/local/bin/opencode', argv: ['/usr/local/bin/opencode', 'serve', '--service'] }),
    sessions: async () => [{ agent_id: ref.agentId, harness: 'opencode', native_root: '', native_id: 'ses_owned' }], agent: async () => row,
    read: async route => route === '/api/info' ? { status: 200, body: { pid: 42 } } : { status, body: session } };
}
const nativeInput = NativeInput.parse({ agent: ref, view: 'presence', nativeSessionId: 'ses_owned', nativeRoot: '', expectedGeneration: 'gen_1', maxMessages: 200 });
const present = { data: { id: 'ses_owned', metadata: { agent_id: ref.agentId }, location: { directory: row.worktree_path } } };
const absent = { _tag: 'SessionNotFoundError', sessionID: 'ses_owned', message: 'Session not found: ses_owned' };
test('native presence preserves empty root and exact missing-session semantics', async () => {
  assert.equal((await observeNative(nativeInput, access(present), row, signal)).view, 'presence');
  const missing = await observeNative(nativeInput, access(absent, 404), row, signal);
  assert.ok(missing.view === 'presence'); assert.equal(missing.present, false); assert.equal(missing.nativeRoot, '');
});
for (const [name, body, status] of [
  ['route404', {}, 404], ['wrong-tag', { ...absent, _tag: 'NotFoundError' }, 404], ['foreign-id', { ...absent, sessionID: 'foreign' }, 404],
  ['wrong-message', { ...absent, message: 'gone' }, 404], ['data-on-missing', { ...absent, data: {} }, 404], ['auth', {}, 401], ['error', {}, 500],
  ['foreign-session', { data: { ...present.data, id: 'foreign' } }, 200],
] as const) test(`native absence rejects ${name}`, async () => { await assert.rejects(observeNative(nativeInput, access(body, status), row, signal)); });
test('native rejects duplicate registry and stale generation', async () => {
  const native = access(present); const sessions = native.sessions;
  native.sessions = async id => [...await sessions(id), ...await sessions(id)];
  await assert.rejects(observeNative(nativeInput, native, row, signal));
  await assert.rejects(observeNative({ ...nativeInput, expectedGeneration: 'stale' }, access(present), row, signal));
});
test('completed model observations are independent of expected model', async () => {
  const native = access(present); const read = native.read;
  native.read = async (route, abort) => route.includes('/message?') ? { status: 200, body: { data: [
    { id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: { completed: 1 }, finish: 'stop', model: { providerID: 'provider', id: 'actual-model' } },
  ] } } : read(route, abort);
  const output = await observeNative({ ...nativeInput, view: 'completed-models' }, native, row, signal);
  assert.ok(output.view === 'completed-models'); assert.equal(output.records[0]!.model, 'actual-model'); assert.notEqual(output.records[0]!.model, 'desired-model');
});
test('filesystem retains exact Unicode bytes; refuses symlink, traversal, duplicates and overflow', async () => {
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-adapter-fs-')));
  try {
    await writeFile(path.join(directory, 'owned.txt'), 'line one\n🙂\r\n'); await symlink('/etc', path.join(directory, 'foreign'));
    const input = FilesystemInput.parse({ leaseId: 'lease', rootId: 'repo', relativePaths: ['owned.txt', 'absent'], view: 'bytes', maxBytes: 1000, maxEntries: 10 });
    const observed = await observeFilesystem(input, directory);
    assert.equal(Buffer.from(observed.entries[0]!.contentBase64!, 'base64').toString('utf8'), 'line one\n🙂\r\n'); assert.equal(observed.entries[1]!.exists, false);
    await assert.rejects(observeFilesystem({ ...input, relativePaths: ['foreign/passwd'] }, directory));
    assert.equal(FilesystemInput.safeParse({ ...input, relativePaths: ['../foreign'] }).success, false);
    await assert.rejects(observeFilesystem({ ...input, relativePaths: ['owned.txt', 'owned.txt'] }, directory));
    await assert.rejects(observeFilesystem({ ...input, maxBytes: 1 }, directory));
  } finally { await rm(directory, { recursive: true }); }
});
test('Files public observations reject wrong path/version and truncation', async () => {
  const content = '🙂\n'; const hash = await sha256(content);
  const body = { path: 'README.md', content, size: Buffer.byteLength(content), binary: false, truncated: false, version: `sha256:${hash}` };
  const input = { agent: ref, path: 'README.md', view: 'content' as const, maxBytes: 1000 };
  assert.equal((await observeFiles(input, '/owned/source', async route => { assert.match(route, /scope=agent&target=agt_owned/); return { status: 200, body }; }, signal)).content, content);
  for (const changed of [{ ...body, path: 'foreign' }, { ...body, version: 'stale' }, { ...body, truncated: true }])
    await assert.rejects(observeFiles(input, '/owned/source', async () => ({ status: 200, body: changed }), signal));
});
