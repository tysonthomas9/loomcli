import test from 'node:test';
import assert from 'node:assert/strict';
import { PassThrough, Writable } from 'node:stream';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { NativeSessionStream } from './native-session-stream.js';
import { NativeReplyFrame, NativeSessionLimits, type NativeSessionGuard } from './native-session-boundaries.js';
import { captureNativeQuerySession } from './native-session.js';
import { captureNativeStore } from './native-store.js';

const signal = () => new AbortController().signal;
const binding = 'retained-workspace';
const query = { operation: 'native-info' as const, workspaceBindingId: binding };
function deferred<T = void>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const framed = (value: unknown) => {
  const bytes = Buffer.from(JSON.stringify(value)), frame = Buffer.alloc(bytes.length + 4);
  frame.writeUInt32BE(bytes.length); frame.set(bytes, 4); return frame;
};
function setup(output: Writable = new PassThrough()) {
  const input = new PassThrough(), ready = deferred(), frameStarted = deferred<AbortController>(), guards: AbortController[] = [];
  let cleanup!: () => Promise<void>, opens = 0, closes = 0, reads = 0, closed = false, failClose = false;
  const observed: AbortSignal[] = [];
  let readHook: ((signal: AbortSignal) => Promise<void>) | undefined;
  const pendingReads = new Set<Promise<unknown>>();
  const guard: NativeSessionGuard = { begin(caller) {
    const timer = new AbortController(); guards.push(timer);
    if (guards.length === 2) frameStarted.resolve(timer);
    return { signal: AbortSignal.any([caller, timer.signal]), release() {} };
  } };
  const stream = new NativeSessionStream(input, output, callback => { cleanup = callback; }, guard);
  const owner = {
    query(_request: unknown, caller: AbortSignal) {
      const raw = Promise.resolve().then(async () => {
        reads++; observed.push(caller); if (readHook) await readHook(caller); caller.throwIfAborted();
        return { workspaceBindingId: binding, operation: 'native-info' as const, data: { status: 200, body: { pid: 42 } } };
      });
      pendingReads.add(raw); return raw.finally(() => { pendingReads.delete(raw); });
    },
    async dispose() {
      if (closed) return;
      await Promise.allSettled([...pendingReads]); if (closed) return;
      if (failClose) { failClose = false; throw Error('exact retained close failed'); }
      closed = true; closes++;
    },
  };
  const factory = async (_caller: AbortSignal, enroll: (cleanup: () => Promise<void>) => void) => {
    assert.equal(typeof cleanup, 'function'); enroll(owner.dispose); opens++; ready.resolve(); return owner;
  };
  return { input, output, owner, stream, guards, ready, frameStarted, factory, observed,
    verify: async () => {}, cleanup: () => cleanup(), opens: () => opens, closes: () => closes, reads: () => reads,
    failClose() { failClose = true; }, setReadHook(hook: typeof readHook) { readHook = hook; } };
}

test('one fixed factory handles sequential framed queries, then closes descriptors before successful shutdown', async () => {
  const s = setup(), replies: unknown[] = [], received = deferred(); let decoder = new NativeReplyFrame();
  s.output.on('data', (chunk: Buffer) => {
    const payload = decoder.push(chunk); if (!payload) return;
    decoder.end(); replies.push(JSON.parse(payload.toString())); decoder = new NativeReplyFrame();
    if (replies.length === 1) { s.input.write(framed({ sequence: 2, query })); }
    if (replies.length === 2) { s.input.write(framed({ sequence: 3, workspaceBindingId: binding, shutdown: true })); }
    if (replies.length === 3) { assert.equal(s.closes(), 1); received.resolve(); }
  });
  try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); await s.ready.promise;
    const first = framed({ sequence: 1, query }); s.input.write(first.subarray(0, 2)); s.input.write(first.subarray(2));
    await received.promise; const receipt = await running;
    assert.equal(s.opens(), 1); assert.equal(s.reads(), 2); assert.equal(s.closes(), 1);
    assert.equal(receipt.completed, true); assert.equal(receipt.descriptorsClosed, true); assert.equal(receipt.wire?.poisoned, false);
    assert.deepEqual(replies[2], { sequence: 3, workspaceBindingId: binding, closed: true });
    assert.equal(receipt.rawInputBytes, first.length + framed({ sequence: 2, query }).length + framed({ sequence: 3, workspaceBindingId: binding, shutdown: true }).length);
    await s.cleanup(); await s.cleanup(); assert.equal(s.closes(), 1); assert.equal(s.input.closed, true); assert.equal(s.output.closed, true);
  } finally { await s.cleanup(); }
});

for (const kind of ['empty-eof', 'partial-eof', 'extra-frame', 'string', 'oversized-raw', 'foreign'] as const)
  test(`${kind} never becomes missing data or a successful shutdown`, async () => {
    const s = setup(); let writes = 0; s.output.on('data', () => { writes++; });
    try {
      if (kind === 'string') s.input.setEncoding('utf8');
      const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
      if (kind === 'empty-eof') s.input.end();
      if (kind === 'partial-eof') s.input.end(Buffer.from([0, 0, 0]));
      if (kind === 'extra-frame') s.input.write(Buffer.concat([framed({ sequence: 1, query }), framed({ sequence: 2, query })]));
      if (kind === 'string') s.input.write('foreign text');
      if (kind === 'oversized-raw') s.input.write(Buffer.alloc(NativeSessionLimits.input + 1));
      if (kind === 'foreign') s.input.write(framed({ sequence: 1, query: { ...query, workspaceBindingId: 'foreign' } }));
      await rejected; assert.equal(s.reads(), 0); assert.equal(writes, 0); assert.equal(s.stream.receipt().completed, false);
      if (kind === 'partial-eof') assert.equal(s.stream.receipt().rawInputBytes, 3);
      if (kind === 'oversized-raw') assert.equal(s.stream.receipt().rawInputBytes, NativeSessionLimits.input + 1);
      await s.cleanup(); assert.equal(s.closes(), 1);
    } finally { await s.cleanup(); }
  });

test('one frame guard covers all fragments without renewed deadlines', async () => {
  const s = setup(); try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
    s.input.write(Buffer.from([0])); const timer = await s.frameStarted.promise;
    assert.equal(s.guards.length, 2); timer.abort(); await rejected;
    assert.equal(s.stream.receipt().rawInputBytes, 1); assert.equal(s.reads(), 0);
    await s.cleanup(); assert.equal(s.closes(), 1);
  } finally { await s.cleanup(); }
});

test('cleanup aborts the same active frame, joins noncooperative raw query and retries exact close', async () => {
  const s = setup(), entered = deferred(), release = deferred(); let actualFrame!: AbortSignal;
  s.setReadHook(async caller => { actualFrame = caller; entered.resolve(); await release.promise; });
  try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
    s.input.write(framed({ sequence: 1, query })); await entered.promise;
    s.failClose(); const first = s.cleanup(); const closeRejected = assert.rejects(first);
    assert.equal(actualFrame.aborted, true); await rejected; assert.equal(s.closes(), 0);
    release.resolve(); await closeRejected;
    await s.cleanup(); assert.equal(s.closes(), 1); assert.equal(s.opens(), 1); assert.equal(s.reads(), 1);
  } finally { release.resolve(); await s.cleanup(); }
});

test('deadline returns incomplete while raw write remains retained until its real callback', async () => {
  let writeCallback: ((error?: Error | null) => void) | undefined;
  let writeReleased = false;
  const finishWrite = () => { if (!writeReleased && writeCallback) { writeReleased = true; writeCallback(); } };
  const entered = deferred(), output = new Writable({ write(_chunk, _encoding, callback) { writeCallback = callback; entered.resolve(); } });
  const s = setup(output);
  try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
    s.input.write(framed({ sequence: 1, query })); await entered.promise;
    s.guards[1]!.abort(); await rejected;
    const closing = s.cleanup(); assert.equal(s.closes(), 0); assert.equal(s.stream.receipt().completed, false);
    finishWrite(); await closing; assert.equal(s.closes(), 1); assert.equal(s.reads(), 1);
  } finally { if (writeCallback) finishWrite(); await s.cleanup(); }
});

test('late factory return after acquisition cancellation stays retained and closes once', async () => {
  const s = setup(), entered = deferred(), release = deferred(), resourceReturned = deferred(), caller = new AbortController();
  try {
    const factory = async (_signal: AbortSignal, enroll: (cleanup: () => Promise<void>) => void) => {
      enroll(async () => { await resourceReturned.promise; await s.owner.dispose(); });
      entered.resolve(); await release.promise; resourceReturned.resolve(); return s.owner;
    };
    const running = s.stream.run(binding, factory, s.verify, caller.signal); const rejected = assert.rejects(running); await entered.promise;
    caller.abort(); await rejected;
    const closing = s.cleanup(); assert.equal(s.closes(), 0); release.resolve(); await closing;
    assert.equal(s.closes(), 1); assert.equal(s.reads(), 0); await s.cleanup(); assert.equal(s.closes(), 1);
  } finally { release.resolve(); await s.cleanup(); }
});

test('synchronous caller abort plus rejected verification is observed before fresh cleanup', async () => {
  const s = setup(), caller = new AbortController();
  try {
    await assert.rejects(s.stream.run(binding, s.factory, async () => {
      caller.abort(); return Promise.reject(Error('actual startup verification rejection'));
    }, caller.signal));
    await new Promise<void>(resolve => setImmediate(resolve)); await s.cleanup(); assert.equal(s.opens(), 0);
  } finally { await s.cleanup(); }
});

test('fresh cleanup joins an expired old cleanup then retries the same failed owner close', async () => {
  const s = setup(), entered = deferred(), release = deferred();
  s.setReadHook(async () => { entered.resolve(); await release.promise; });
  try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
    s.input.write(framed({ sequence: 1, query })); await entered.promise; s.failClose();
    const first = s.cleanup(); const firstRejected = assert.rejects(first); s.guards[2]!.abort(); await firstRejected; await rejected;
    assert.equal(s.closes(), 0);
    const fresh = s.cleanup(); release.resolve(); await fresh;
    assert.equal(s.closes(), 1); assert.equal(s.reads(), 1); assert.equal(s.opens(), 1);
  } finally { release.resolve(); await s.cleanup(); }
});

test('preaborted and denied preflight never invoke the fixed factory', async () => {
  for (const kind of ['abort', 'denied'] as const) {
    const s = setup(); try {
      await assert.rejects(s.stream.run(binding, s.factory, async () => { throw Error('authority/build denied'); },
        kind === 'abort' ? AbortSignal.abort() : signal()));
      await s.cleanup(); assert.equal(s.opens(), 0); assert.equal(s.reads(), 0); assert.equal(s.closes(), 0);
    } finally { await s.cleanup(); }
  }
});

test('failed shutdown write cannot mark completion even though descriptor close succeeded', async () => {
  const output = new Writable({ write(_chunk, _encoding, callback) { callback(Error('uncertain shutdown output')); } });
  const s = setup(output); try {
    const running = s.stream.run(binding, s.factory, s.verify, signal()); const rejected = assert.rejects(running); await s.ready.promise;
    s.input.write(framed({ sequence: 1, workspaceBindingId: binding, shutdown: true })); await rejected;
    assert.equal(s.closes(), 1); assert.equal(s.stream.receipt().descriptorsClosed, true); assert.equal(s.stream.receipt().completed, false);
    await s.cleanup(); assert.equal(s.closes(), 1);
  } finally { await s.cleanup(); }
});

test('fixed stream factory composes the real captured query owner and retains one store across reads', async () => {
  const root = await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-stream-'));
  const input = new PassThrough(), output = new PassThrough(), callbacks: (() => Promise<void>)[] = [];
  const guard = { begin(signal: AbortSignal) { return { signal, release() {} }; } };
  try {
    const config = path.join(root, 'configuration'), repo = path.join(root, 'beta'), common = path.join(repo, '.git');
    for (const directory of [config, repo, common]) await fs.mkdir(directory);
    const stamp = async (path: string) => { const stat = await fs.lstat(path); return { path, device: stat.dev, inode: stat.ino }; };
    const configurationRoot = await stamp(config), filename = path.join(config, 'agents.db'), db = new DatabaseSync(filename);
    db.exec('CREATE TABLE actual_test_marker (value TEXT)'); db.close();
    const expected = await captureNativeStore(configurationRoot, () => {}, signal()); await expected.close();
    const directory = path.join(config, 'agents-opencode/state/opencode'); await fs.mkdir(directory, { recursive: true });
    await fs.writeFile(path.join(directory, 'service.json'), JSON.stringify({ pid: 42, url: 'http://127.0.0.1:4123/', password: 'private-test-secret' }));
    let opens = 0, closes = 0, reads = 0;
    const files = { ...fs, async open(...args: Parameters<typeof fs.open>) {
      opens++; assert.equal(callbacks.length, 1); const handle = await fs.open(...args), close = handle.close.bind(handle);
      handle.close = async () => { closes++; await close(); }; return handle;
    } };
    const repository = { repoName: 'beta', sourceRepoId: 'actual-beta', repo, commonDir: common, groups: [] };
    const sourceRoot = await stamp(repo), commonRoot = await stamp(common);
    const stream = new NativeSessionStream(input, output, cleanup => callbacks.push(cleanup), guard);
    let decoder = new NativeReplyFrame(), replies = 0;
    output.on('data', (chunk: Buffer) => {
      const payload = decoder.push(chunk); if (!payload) return;
      decoder.end(); decoder = new NativeReplyFrame(); replies++;
      const reply = JSON.parse(payload.toString());
      assert.equal(JSON.stringify(reply).includes('private-test-secret'), false);
      if (replies < 3) input.write(framed({ sequence: replies + 1, query }));
      else if (replies === 3) input.write(framed({ sequence: 4, workspaceBindingId: binding, shutdown: true }));
      else { assert.equal(reply.closed, true); assert.equal(closes, 4); }
    });
    const ready = deferred();
    const running = stream.run(binding, async (caller, enrollCleanup) => {
      const owner = await captureNativeQuerySession({ configurationRoot, workspaceBindingId: binding,
        workspace: { workspaceId: 'ACTUAL-WS', repo, storeId: expected.storeId, storeGeneration: expected.storeGeneration },
        sources: [{ sourceKey: 'beta-source', repository, root: sourceRoot }], commonDirectories: [commonRoot],
        pinnedExecutable: '/pinned/opencode', secrets: [], enrollCleanup, verifyNamespace: async () => {},
        processIdentity: async pid => ({ pid, generation: 'actual-kernel-test-port', executable: '/pinned/opencode', argv: ['/pinned/opencode', 'serve', '--service'] }),
        fetch: async () => { reads++; return new Response(JSON.stringify({ pid: 42 })); },
        physical: { async source() { throw Error('not selected'); }, async agent() { throw Error('not selected'); } },
      }, caller, files, guard);
      ready.resolve(); return owner;
    }, async (_caller, phase) => { if (phase === 'descriptors-closed') assert.equal(closes, 4); }, signal());
    await ready.promise; input.write(framed({ sequence: 1, query }));
    const receipt = await running; assert.equal(receipt.completed, true); assert.equal(replies, 4);
    assert.equal(opens, 4); assert.equal(closes, 4); assert.equal(reads, 3);
    await stream.dispose(); await callbacks[0]!(); assert.equal(closes, 4);
  } finally { for (const cleanup of callbacks) await cleanup(); await fs.rm(root, { recursive: true, force: true }); }
});
