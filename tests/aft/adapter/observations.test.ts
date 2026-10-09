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
  const actual = await observeFiles(input, '/owned/source', async route => { assert.match(route, /scope=agent&target=agt_owned/); return { status: 200, body }; }, signal);
  assert.ok(actual.view === 'content'); assert.equal(actual.content, content);
  for (const changed of [{ ...body, path: 'foreign' }, { ...body, version: 'stale' }, { ...body, truncated: true }])
    await assert.rejects(observeFiles(input, '/owned/source', async () => ({ status: 200, body: changed }), signal));
});

test('Files stat reports only actual metadata, and binary content does not fabricate text', async () => {
  const input = { agent: ref, path: 'file', view: 'stat' as const, maxBytes: 1000 };
  const stat = await observeFiles(input, '/owned/source', async () => ({ status: 200, body:
    { path: 'file', size: 17, version: 'strong-version', is_dir: false, mod_time: '2026-10-09T00:00:00Z' } }), signal);
  assert.ok(stat.view === 'stat'); assert.equal(stat.isDirectory, false); assert.equal(stat.modifiedAt, '2026-10-09T00:00:00Z');
  assert.ok(!('binary' in stat)); assert.ok(!('truncated' in stat));
  await assert.rejects(observeFiles(input, '/owned/source', async () => ({ status: 200, body: { path: 'file', size: 17, version: 'v' } }), signal));
  const binary = await observeFiles({ ...input, view: 'content' }, '/owned/source', async () => ({ status: 200, body:
    { path: 'file', size: 17, version: 'strong-version', binary: true, truncated: false } }), signal);
  assert.ok(binary.view === 'content'); assert.equal(binary.content, null); assert.equal(binary.sha256, null);
});

test('synthetic native tool facts distinguish leaks, wrong probe, duplicates and redacted copies without expecting pass', async () => {
  const { createSyntheticProbe } = await import('./synthetic-probe.js');
  const probe = createSyntheticProbe('run_owned', 'lease');
  const native = access(present); const read = native.read;
  const tool = (id: string, input: Json, output: Json) => ({ type: 'tool', id, name: 'bash', state: { status: 'completed', input, content: output } });
  let content: Json[] = [tool('call_1', { command: `printf SAFE # TOKEN=${probe.value}` }, { text: 'SAFE' })];
  native.read = async (route, abort) => route.includes('/message?') ? { status: 200, body: { data: [{ id: 'msg_1', sessionID: 'ses_owned',
    type: 'assistant', time: { completed: 1 }, content }] } } : read(route, abort);
  const input = { ...nativeInput, view: 'tools' as const, probeHandle: probe.handle };
  const actual = await observeNative(input, native, row, signal, probe);
  assert.ok(actual.view === 'tools'); assert.equal(actual.records[0]!.probe!.inputOccurrences, 1); assert.equal(actual.records[0]!.probe!.outputOccurrences, 0);
  content = [tool('call_1', { command: probe.value }, { text: probe.value })];
  const leaking = await observeNative(input, native, row, signal, probe);
  assert.ok(leaking.view === 'tools'); assert.equal(leaking.records[0]!.probe!.outputOccurrences, 1);
  content = [tool('call_1', { command: 'ghp_AFTONLYforeignQ7mR2pK9xT4vN8cY6bL5fS3dH1jW0' }, { text: 'SAFE' })];
  const wrong = await observeNative(input, native, row, signal, probe);
  assert.ok(wrong.view === 'tools'); assert.equal(wrong.records[0]!.probe!.inputOccurrences, 0);
  content = [tool('call_1', { command: '[REDACTED]' }, { text: 'SAFE' })];
  const redactedCopy = await observeNative(input, native, row, signal, probe);
  assert.ok(redactedCopy.view === 'tools'); assert.equal(redactedCopy.records[0]!.probe!.inputOccurrences, 0);
  content = [tool('call_1', { command: probe.value }, { text: 'SAFE' }), tool('call_2', { command: probe.value }, { text: 'SAFE' })];
  const duplicateMatches = await observeNative(input, native, row, signal, probe);
  assert.ok(duplicateMatches.view === 'tools'); assert.equal(duplicateMatches.records.filter(record => record.probe?.inputOccurrences === 1).length, 2);
  const partial = await observeNative({ ...input, maxMessages: 1 }, native, row, signal, probe);
  assert.ok(partial.view === 'tools'); assert.equal(partial.complete, false);
  await assert.rejects(observeNative({ ...input, probeHandle: 'foreign' }, native, row, signal, probe));
  content = [{ type: 'tool', id: 'call_1', name: 'bash', state: { status: 'completed', input: {} } }];
  await assert.rejects(observeNative(input, native, row, signal, probe));
});
test('saved-event pre-redaction occurrence facts cannot conceal a product leak', async () => {
  const { createSyntheticProbe } = await import('./synthetic-probe.js'); const probe = createSyntheticProbe('run_owned', 'lease');
  const source = { ...event(1), payload: { text: `unexpected leak ${probe.value}` } };
  const actual = await collectSavedEvents({ ...eventsInput, probeHandle: probe.handle }, async () => ({ status: 200, body:
    { events: [source], snapshot_seq: 1, next: 1, more: false } }), signal, probe);
  assert.equal(actual.events[0]!.probe!.payloadOccurrences, 1);
  await assert.rejects(collectSavedEvents({ ...eventsInput, probeHandle: 'foreign' }, async () => ({ status: 200, body: {} }), signal, probe));
});

test('native registrations preserve all historically owned identities and exact deletion semantics', async () => {
  const native = access(present); const read = native.read;
  native.sessions = async () => [
    {agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_owned'},
    {agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_historical'},
  ];
  const registered = await observeNative({...nativeInput,view:'registrations'},native,row,signal);
  assert.ok(registered.view==='registrations'); assert.equal(registered.currentNativeSessionId,'ses_owned');
  assert.deepEqual(registered.records.map(record=>record.nativeSessionId),['ses_historical','ses_owned']);
  native.read = async(route,abort)=>route==='/api/session/ses_historical' ? {status:404,body:{_tag:'SessionNotFoundError',sessionID:'ses_historical',message:'Session not found: ses_historical'}} : read(route,abort);
  const old = await observeNative({...nativeInput,nativeSessionId:'ses_historical'},native,row,signal);
  assert.ok(old.view==='presence'); assert.equal(old.present,false); assert.equal(old.nativeSessionId,'ses_historical');
  await assert.rejects(observeNative({...nativeInput,nativeSessionId:'ses_foreign'},native,row,signal));
});
test('registration observations reject incomplete bounds, missing current owner and duplicate historical identity', async () => {
  const native = access(present);
  native.sessions = async () => [
    {agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_owned'},
    {agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_old'},
  ];
  await assert.rejects(observeNative({...nativeInput,view:'registrations',maxRegistrations:1},native,row,signal),/bound reached/);
  native.sessions = async () => [{agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_old'}];
  await assert.rejects(observeNative({...nativeInput,view:'registrations'},native,row,signal),/current registration/);
  native.sessions = async () => [{agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_owned'},
    {agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_old'},{agent_id:ref.agentId,harness:'opencode',native_root:'',native_id:'ses_old'}];
  await assert.rejects(observeNative({...nativeInput,view:'registrations'},native,row,signal),/duplicated/);
});

test('captured historical selectors reject current-only omission, wrong root and changed endpoint/process', async () => {
  const native = access(present);
  const prior = [
    {agent_id:ref.agentId,harness:'opencode' as const,native_root:'',native_id:'ses_owned'},
    {agent_id:ref.agentId,harness:'opencode' as const,native_root:'',native_id:'ses_old'},
  ];
  native.sessions = async()=>prior;
  const captured = await observeNative({...nativeInput,view:'registrations'},native,row,signal);
  assert.ok(captured.view==='registrations');
  const selected = {...nativeInput,nativeSessionId:captured.records[0]!.nativeSessionId,nativeRoot:captured.records[0]!.nativeRoot,
    expectedEndpointId:captured.registeredEndpointId,expectedServicePid:captured.servicePid};
  native.sessions = async()=>[prior[0]!];
  await assert.rejects(observeNative(selected,native,row,signal),/registration is missing/);
  native.sessions = async()=>[prior[0]!,{...prior[1]!,native_root:'foreign-root'}];
  await assert.rejects(observeNative(selected,native,row,signal),/registration is missing/);
  native.sessions = async()=>prior;
  await assert.rejects(observeNative({...selected,expectedEndpointId:'changed'},native,row,signal),/captured endpoint/);
  await assert.rejects(observeNative({...selected,expectedServicePid:43},native,row,signal),/captured endpoint/);
  await assert.rejects(observeNative({...selected,expectedGeneration:'changed'},native,row,signal),/process changed/);
  native.sessions = async()=>[{...prior[0]!,agent_id:'agt_foreign'},prior[1]!];
  await assert.rejects(observeNative(selected,native,row,signal),/current registration/);
});
