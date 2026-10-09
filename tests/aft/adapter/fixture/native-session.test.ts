import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { captureNativeQuerySession, type NativeQuerySessionCoordinates } from './native-session.js';
import { captureNativeStore } from './native-store.js';

const signal = () => new AbortController().signal;
const unboundedTestGuard = { begin(signal: AbortSignal) { return { signal, release() {} }; } };
function deferred() { let release!: () => void; const promise = new Promise<void>(resolve => { release = resolve; }); return { promise, release }; }
async function setup() {
  const root = await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-session-'));
  const config = path.join(root, 'configuration'), source = path.join(root, 'beta');
  const common = path.join(source, '.git'), worktree = path.join(root, 'agt_beta');
  for (const directory of [config, source, common, worktree]) await fs.mkdir(directory);
  const stamp = async (directory: string) => {
    const stat = await fs.lstat(directory); return { path: directory, device: stat.dev, inode: stat.ino };
  };
  const filename = path.join(config, 'agents.db'), db = new DatabaseSync(filename);
  db.exec(`CREATE TABLE agents (agent_id TEXT, workspace_id TEXT, repo TEXT, worktree_path TEXT, branch TEXT, harness TEXT,
    harness_session_id TEXT, harness_session_root TEXT, parent_agent_id TEXT, root_agent_id TEXT, created_by_kind TEXT, created_by_id TEXT,
    preset TEXT, revision INTEGER, state TEXT, running_turn_id TEXT, deleted_at TEXT, history_purged_at TEXT, model TEXT, outcome TEXT,
    name TEXT, created_at TEXT);
    CREATE TABLE agent_native_sessions (agent_id TEXT, harness TEXT, native_root TEXT, native_id TEXT);`);
  db.prepare('INSERT INTO agents VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run('agt_beta', 'NATIVE-WS', source,
    worktree, 'loom/agent/beta', 'opencode', 'ses_beta', '', null, null, 'user', 'actual-user', 'lead', 4, 'idle',
    null, null, null, 'observed/model', null, 'source-native-name', '2026-10-09T00:00:00Z');
  db.exec("INSERT INTO agent_native_sessions VALUES ('agt_beta','opencode','','ses_beta')"); db.close();
  const registrationParent = path.join(config, 'agents-opencode/state/opencode'); await fs.mkdir(registrationParent, { recursive: true });
  const registrationFile = path.join(registrationParent, 'service.json');
  await fs.writeFile(registrationFile, JSON.stringify({ pid: 42, url: 'http://127.0.0.1:4123/', password: 'private-password' }));
  const configRoot = await stamp(config), sourceRoot = await stamp(source), commonRoot = await stamp(common), worktreeRoot = await stamp(worktree);
  // Physical setup control supplies expected canonical store coordinates; it
  // is not creator continuity or production workspace creation evidence.
  const expected = await captureNativeStore(configRoot, () => {}, signal()); await expected.close();
  const callbacks: (() => Promise<void>)[] = [], opens: string[] = [], closes: string[] = [], namespaceSignals: AbortSignal[] = [];
  let namespaceValid = true, closeFailure = false;
  let namespaceHook: ((signal: AbortSignal) => Promise<void>) | undefined;
  let fetchHook: (() => Promise<void>) | undefined;
  const routes: string[] = [];
  const repository = { repoName: 'beta', sourceRepoId: 'actual-beta-id', repo: source, commonDir: common, groups: ['shared'] };
  const coordinates: NativeQuerySessionCoordinates = {
    configurationRoot: configRoot, workspaceBindingId: 'captured-workspace-binding',
    workspace: { workspaceId: 'NATIVE-WS', repo: source, storeId: expected.storeId, storeGeneration: expected.storeGeneration },
    sources: [{ sourceKey: 'beta-source', repository, root: sourceRoot }], commonDirectories: [commonRoot],
    pinnedExecutable: '/pinned/opencode', secrets: [],
    async verifyNamespace(abort) {
      namespaceSignals.push(abort); assert.equal(callbacks.length, 1); abort.throwIfAborted();
      if (namespaceHook) await namespaceHook(abort);
      assert.equal(namespaceValid, true, 'retained namespace/helper generation changed'); abort.throwIfAborted();
    },
    enrollCleanup(cleanup) { callbacks.push(cleanup); },
    processIdentity: async pid => ({ pid, generation: 'actual-native-generation', executable: '/pinned/opencode', argv: ['/pinned/opencode', 'serve', '--service'] }),
    async fetch(input, init) {
      assert.equal(new Headers(init?.headers).get('Authorization'), `Basic ${Buffer.from('opencode:private-password').toString('base64')}`);
      const url = new URL(String(input)); routes.push(url.pathname + url.search);
      if (fetchHook) await fetchHook();
      const body = url.pathname === '/api/info' ? { pid: 42 } : url.search ?
        [{ id: 'msg_saved', info: { time: { completed: 42 }, finish: 'stop', model: { modelID: 'actual/model' } } }] : { id: 'ses_beta' };
      return new Response(JSON.stringify(body), { status: 200 });
    },
    physical: {
      async source(key, abort) { abort.throwIfAborted(); assert.equal(key, 'beta-source'); return { repository, root: sourceRoot }; },
      async agent(id, abort) { abort.throwIfAborted(); assert.equal(id, 'agt_beta'); return { agentId: id, root: worktreeRoot, commonDir: common }; },
    },
  };
  const files = { ...fs, async open(...args: Parameters<typeof fs.open>) {
    assert.equal(callbacks.length, 1); opens.push(String(args[0])); const handle = await fs.open(...args);
    return new Proxy(handle, { get(target, key) {
      if (key === 'close') return async () => {
        if (closeFailure && String(args[0]) === filename) { closeFailure = false; throw Error('injected exact descriptor close failure'); }
        closes.push(String(args[0])); await target.close();
      };
      const value = Reflect.get(target, key); return typeof value === 'function' ? value.bind(target) : value;
    } });
  } };
  const request = (operation: string, fields = {}) => ({ workspaceBindingId: 'captured-workspace-binding', operation, ...fields });
  return { root, config, source, common, filename, coordinates, callbacks, files, opens, closes, routes, namespaceSignals, request,
    setNamespaceHook(hook: typeof namespaceHook) { namespaceHook = hook; }, setFetchHook(hook: typeof fetchHook) { fetchHook = hook; },
    replaceNamespace() { namespaceValid = false; }, failClose() { closeFailure = true; },
    async remove() { for (const cleanup of callbacks) await cleanup(); await fs.rm(root, { recursive: true, force: true }); } };
}

test('one descriptor owner delegates actual SQLite identities, refs and assistant prefix without recapture', async () => {
  const s = await setup(); try {
    const acquisition = new AbortController();
    const session = await captureNativeQuerySession(s.coordinates, acquisition.signal, s.files, unboundedTestGuard);
    assert.deepEqual(s.opens, [s.config, s.source, s.common, s.filename]);
    // The acquisition signal is not the lifetime signal of later queries.
    acquisition.abort(); s.namespaceSignals.length = 0;
    const caller = signal();
    const actual = await session.query(s.request('agent-identity', { agentId: 'agt_beta' }), caller);
    assert.equal(actual.operation, 'agent-identity');
    if (actual.operation === 'agent-identity') {
      assert.equal(actual.data.name, 'source-native-name'); assert.equal(actual.data.created_at, '2026-10-09T00:00:00Z');
      assert.equal(actual.data.repo, s.source);
    }
    assert.ok(s.namespaceSignals.length > 2); assert.ok(s.namespaceSignals.every(value => value === caller));
    const update = new DatabaseSync(s.filename); update.exec('UPDATE agents SET revision=5'); update.close();
    const row = await session.query(s.request('raw-agent', { agentId: 'agt_beta' }), signal());
    if (row.operation === 'raw-agent') assert.equal(row.data.revision, 5); else assert.fail('wrong operation');
    const refs = await session.query(s.request('sessions', { agentId: 'agt_beta', maxRegistrations: 200 }), signal());
    assert.deepEqual(refs.data, [{ agent_id: 'agt_beta', harness: 'opencode', native_root: '', native_id: 'ses_beta' }]);
    const registration = await session.query(s.request('registration-identity'), signal());
    assert.equal('password' in registration.data, false); assert.equal(JSON.stringify(registration).includes('private-password'), false);
    await session.query(s.request('native-assistant-prefix', { agentId: 'agt_beta', nativeSessionId: 'ses_beta', nativeRoot: '' }), signal());
    assert.deepEqual(s.routes, ['/api/session/ses_beta/message?type=assistant&order=desc&limit=200']);
    assert.equal((await session.query(s.request('source-physical', { sourceKey: 'beta-source' }), signal())).operation, 'source-physical');
    assert.equal((await session.query(s.request('agent-physical', { agentId: 'agt_beta' }), signal())).operation, 'agent-physical');
    assert.equal(s.opens.length, 4); await session.dispose(); await s.callbacks[0]!();
    assert.deepEqual(s.closes, [s.filename, s.common, s.source, s.config]);
    await assert.rejects(session.query(s.request('native-info'), signal()));
  } finally { await s.remove(); }
});

for (const replacement of ['store', 'source', 'namespace'] as const) test(`${replacement} replacement invalidates rather than reopens the binding`, async () => {
  const s = await setup(); try {
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, unboundedTestGuard);
    if (replacement === 'store') { await fs.rename(s.filename, s.filename + '.retained'); const db = new DatabaseSync(s.filename); db.close(); }
    if (replacement === 'source') { await fs.rename(s.source, s.source + '.retained'); await fs.mkdir(s.source); }
    if (replacement === 'namespace') s.replaceNamespace();
    await assert.rejects(session.query(s.request('raw-agent', { agentId: 'agt_beta' }), signal()));
    assert.equal(s.opens.length, 4); assert.equal(s.routes.length, 0);
    await session.dispose(); assert.equal(s.closes.length, 4);
  } finally { await s.remove(); }
});

test('abort returns without replay; enrolled cleanup joins raw work before descriptor close and retries exact failed close', async () => {
  const s = await setup(); const entered = deferred(), release = deferred(); try {
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, unboundedTestGuard);
    s.setFetchHook(async () => { entered.release(); await release.promise; });
    const caller = new AbortController();
    const reading = session.query(s.request('native-info'), caller.signal); const rejected = assert.rejects(reading);
    await entered.promise; caller.abort(); await rejected;
    await assert.rejects(session.query(s.request('native-info'), signal()));
    s.failClose(); const closing = s.callbacks[0]!(); const closeRejected = assert.rejects(closing);
    assert.equal(s.closes.length, 0); assert.equal(s.routes.length, 1);
    release.release(); await closeRejected; assert.equal(s.closes.length, 0);
    await session.dispose(); assert.equal(s.closes.length, 4); assert.equal(s.routes.length, 1);
  } finally { release.release(); await s.remove(); }
});

test('query reservation excludes another query and disposal prevents successful exposure after raw read', async () => {
  const s = await setup(); const entered = deferred(), release = deferred(); try {
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, unboundedTestGuard);
    s.setNamespaceHook(async () => { entered.release(); await release.promise; });
    const reading = session.query(s.request('raw-agent', { agentId: 'agt_beta' }), signal()); const rejected = assert.rejects(reading);
    await entered.promise; await assert.rejects(session.query(s.request('native-info'), signal()));
    const closing = session.dispose(); assert.equal(s.closes.length, 0);
    release.release(); await rejected; await closing; assert.equal(s.closes.length, 4); assert.equal(s.routes.length, 0);
  } finally { release.release(); await s.remove(); }
});

test('foreign store generation and malformed finite topology deny acquisition with retained cleanup', async () => {
  for (const kind of ['store', 'source'] as const) {
    const s = await setup(); try {
      const coordinates = kind === 'store' ? { ...s.coordinates, workspace: { ...s.coordinates.workspace, storeGeneration: 'foreign' } } :
        { ...s.coordinates, sources: [{ ...s.coordinates.sources[0]!, root: { ...s.coordinates.sources[0]!.root, path: '/foreign' } }] };
      await assert.rejects(captureNativeQuerySession(coordinates, signal(), s.files, unboundedTestGuard));
      if (kind === 'source') assert.equal(s.opens.length, 0);
      else { assert.equal(s.opens.length, 4); await s.callbacks[0]!(); assert.equal(s.closes.length, 4); }
    } finally { await s.remove(); }
  }
});

test('enrolled cleanup excludes new queries synchronously, before its first await', async () => {
  const s = await setup(); try {
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, unboundedTestGuard);
    s.namespaceSignals.length = 0;
    const closing = s.callbacks[0]!();
    await assert.rejects(session.query(s.request('native-info'), signal()));
    assert.equal(s.namespaceSignals.length, 0); assert.equal(s.routes.length, 0);
    await closing; assert.equal(s.closes.length, 4);
  } finally { await s.remove(); }
});

test('late descriptor acquisition is joined by cleanup after abort, without releasing other roots early', async () => {
  const s = await setup(); const entered = deferred(), release = deferred(); try {
    const files = { ...s.files, async open(...args: Parameters<typeof fs.open>) {
      if (String(args[0]) === s.filename) { entered.release(); await release.promise; }
      return s.files.open(...args);
    } };
    const caller = new AbortController();
    const acquisition = captureNativeQuerySession(s.coordinates, caller.signal, files, unboundedTestGuard);
    const rejected = assert.rejects(acquisition); await entered.promise;
    caller.abort(); await rejected;
    const closing = s.callbacks[0]!(); assert.equal(s.closes.length, 0);
    release.release(); await closing; assert.equal(s.opens.length, 4); assert.equal(s.closes.length, 4);
  } finally { release.release(); await s.remove(); }
});

test('manual query deadline retains raw reservation and cleanup until the exact pending read settles', async () => {
  const s = await setup(); const entered = deferred(), release = deferred(), deadline = new AbortController(); try {
    let calls = 0;
    const guard = { begin(caller: AbortSignal) {
      calls++; return { signal: calls === 2 ? AbortSignal.any([caller, deadline.signal]) : caller, release() {} };
    } };
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, guard);
    s.setFetchHook(async () => { entered.release(); await release.promise; });
    const reading = session.query(s.request('native-info'), signal()); const rejected = assert.rejects(reading);
    await entered.promise; deadline.abort(); await rejected;
    const closing = session.dispose(); assert.equal(s.closes.length, 0);
    await assert.rejects(session.query(s.request('native-info'), signal()));
    release.release(); await closing; assert.equal(s.closes.length, 4); assert.equal(s.routes.length, 1);
  } finally { release.release(); await s.remove(); }
});

test('finite alpha/beta topology retains both sources and reads beta without a first-source alias', async () => {
  const s = await setup(); try {
    const alpha = path.join(s.root, 'alpha'), common = path.join(alpha, '.git'); await fs.mkdir(alpha); await fs.mkdir(common);
    const stat = await fs.lstat(alpha), commonStat = await fs.lstat(common);
    const coordinates = { ...s.coordinates,
      sources: [{ sourceKey: 'alpha-source', repository: { repoName: 'alpha', sourceRepoId: 'actual-alpha-id',
        repo: alpha, commonDir: common, groups: ['shared'] }, root: { path: alpha, device: stat.dev, inode: stat.ino } }, ...s.coordinates.sources],
      commonDirectories: [{ path: common, device: commonStat.dev, inode: commonStat.ino }, ...s.coordinates.commonDirectories] };
    const session = await captureNativeQuerySession(coordinates, signal(), s.files, unboundedTestGuard);
    const row = await session.query(s.request('raw-agent', { agentId: 'agt_beta' }), signal());
    if (row.operation === 'raw-agent') assert.equal(row.data.repo, s.source); else assert.fail('wrong operation');
    assert.equal(s.opens.length, 6); assert.ok(s.opens.includes(alpha)); assert.ok(s.opens.includes(s.source));
    const db = new DatabaseSync(s.filename); db.exec("UPDATE agents SET repo='/foreign/repo'"); db.close();
    await assert.rejects(session.query(s.request('raw-agent', { agentId: 'agt_beta' }), signal()));
    await session.dispose(); assert.equal(s.closes.length, 6);
  } finally { await s.remove(); }
});

test('preaborted and foreign binding requests dispatch no query and never invent a replacement store', async () => {
  const s = await setup(); try {
    const session = await captureNativeQuerySession(s.coordinates, signal(), s.files, unboundedTestGuard);
    s.namespaceSignals.length = 0;
    await assert.rejects(session.query(s.request('native-info'), AbortSignal.abort()));
    assert.equal(s.namespaceSignals.length, 0);
    await assert.rejects(session.query({ ...s.request('native-info'), workspaceBindingId: 'foreign' }, signal()));
    assert.equal(s.namespaceSignals.length, 0); assert.equal(s.routes.length, 0); assert.equal(s.opens.length, 4);
    await session.dispose();
  } finally { await s.remove(); }
});
