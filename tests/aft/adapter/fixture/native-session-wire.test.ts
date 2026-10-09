import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { ChildProcess } from 'node:child_process';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { NativeSessionPipe } from './native-session-pipe.js';
import { NativeSessionLimits } from './native-session-boundaries.js';
import { captureNativeStore } from './native-store.js';
import { captureNativeQuerySession } from './native-session.js';
import { createNativeSessionWireClient, createNativeSessionWireHandler, NativeRequestFrame } from './native-session-wire.js';

const signal = () => new AbortController().signal;
const guard = { begin(signal: AbortSignal) { return { signal, release() {} }; } };
const bytes = (value: unknown) => Buffer.from(JSON.stringify(value));
const frame = (payload: Uint8Array) => {
  const result = Buffer.alloc(payload.byteLength + 4); result.writeUInt32BE(payload.byteLength); result.set(payload, 4); return result;
};
const query = { operation: 'native-info' as const, workspaceBindingId: 'actual-workspace-binding' };
function deferred() { let release!: () => void; const promise = new Promise<void>(resolve => { release = resolve; }); return { promise, release }; }
function owner() {
  let reads = 0, closes = 0;
  return { async query() { reads++; return { workspaceBindingId: query.workspaceBindingId, operation: 'native-info' as const,
    data: { status: 200, body: { pid: 42 } } }; }, async dispose() { closes++; }, reads: () => reads, closes: () => closes };
}

test('request decoding rejects its smaller allocation bound, incomplete EOF, multiple frames and invalid UTF8', () => {
  const body = bytes({ sequence: 1, query }), decoder = new NativeRequestFrame(); let result: Buffer | undefined;
  for (const byte of frame(body)) result = decoder.push(Buffer.from([byte]));
  assert.deepEqual(result, body); decoder.end();
  for (const size of [0, NativeSessionLimits.request - 3, NativeSessionLimits.reply - 4, 0xffffffff]) {
    const header = Buffer.alloc(4); header.writeUInt32BE(size); assert.throws(() => new NativeRequestFrame().push(header));
  }
  assert.throws(() => new NativeRequestFrame().push(Buffer.concat([frame(body), frame(body)])));
  const partial = new NativeRequestFrame(); partial.push(Buffer.from([0, 0])); assert.throws(() => partial.end());
  assert.throws(() => new NativeRequestFrame().push(frame(Buffer.from([34, 0xf0, 0x90, 0x80, 34]))));
});

test('handler sequences canonical bodies and acknowledges closure only after exact owner disposal', async () => {
  const target = owner(), replies: Buffer[] = [], phases: string[] = [], caller = signal();
  const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async (actual, phase) => {
    assert.equal(actual, caller); phases.push(phase);
    if (phase === 'descriptors-closed') assert.equal(target.closes(), 1);
  }, async (reply, actual) => { assert.equal(actual, caller); replies.push(reply); });
  await handler.handle(bytes({ sequence: 1, query }), caller);
  await handler.handle(bytes({ sequence: 2, query }), caller);
  await handler.handle(bytes({ sequence: 3, workspaceBindingId: query.workspaceBindingId, shutdown: true }), caller);
  assert.deepEqual(JSON.parse(replies[2]!.subarray(4).toString()), { sequence: 3, workspaceBindingId: query.workspaceBindingId, closed: true });
  assert.equal(target.reads(), 2); assert.equal(target.closes(), 1); assert.equal(handler.receipt().poisoned, false);
  assert.equal(handler.receipt().attemptedFrames, 3); assert.deepEqual(phases.slice(-3), ['active', 'descriptors-closed', 'descriptors-closed']);
  await assert.rejects(handler.handle(bytes({ sequence: 4, query }), caller)); assert.equal(replies.length, 3);
});

for (const invalid of [
  { sequence: 2, query }, { sequence: 1, query: { ...query, workspaceBindingId: 'foreign' } },
  { sequence: 1, query, command: 'arbitrary' }, { sequence: 1, query: { ...query, path: '/unowned' } },
]) test(`foreign/out-of-order/unknown request denies before owner or verification: ${JSON.stringify(invalid)}`, async () => {
  const target = owner(); let verifies = 0, writes = 0;
  const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => { verifies++; }, async () => { writes++; });
  await assert.rejects(handler.handle(bytes(invalid), signal()));
  assert.equal(verifies, 0); assert.equal(target.reads(), 0); assert.equal(target.closes(), 0); assert.equal(writes, 0);
  await assert.rejects(handler.handle(bytes({ sequence: 1, query }), signal()));
});

test('pending disposal failure sends no closed acknowledgement; safe out-of-band cleanup remains exact owner retry', async () => {
  const entered = deferred(), release = deferred(); let closes = 0, writes = 0;
  const target = { ...owner(), async dispose() { closes++; entered.release(); await release.promise; if (closes === 1) throw Error('actual retained close failure'); } };
  const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => {}, async () => { writes++; });
  const closing = handler.handle(bytes({ sequence: 1, workspaceBindingId: query.workspaceBindingId, shutdown: true }), signal());
  const rejected = assert.rejects(closing); await entered.promise; assert.equal(writes, 0);
  release.release(); await rejected; assert.equal(writes, 0); assert.equal(handler.receipt().poisoned, true);
  await assert.rejects(handler.handle(bytes({ sequence: 2, workspaceBindingId: query.workspaceBindingId, shutdown: true }), signal()));
  assert.equal(closes, 1); await target.dispose(); assert.equal(closes, 2);
});

test('reserved final frame permits shutdown after 127 normal frames, not a 128th query or replay', async () => {
  for (const shutdown of [true, false]) {
    const target = owner(); let writes = 0;
    const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => {}, async () => { writes++; });
    for (let sequence = 1; sequence < NativeSessionLimits.frames; sequence++) await handler.handle(bytes({ sequence, query }), signal());
    if (shutdown) await handler.handle(bytes({ sequence: 128, workspaceBindingId: query.workspaceBindingId, shutdown: true }), signal());
    else await assert.rejects(handler.handle(bytes({ sequence: 128, query }), signal()));
    assert.equal(writes, shutdown ? 128 : 127); assert.equal(target.closes(), shutdown ? 1 : 0);
  }
});

for (const wrong of ['sequence', 'binding', 'operation', 'extra'] as const) test(`client rejects ${wrong} response without retry`, async () => {
  let exchanges = 0;
  const client = createNativeSessionWireClient(query.workspaceBindingId, { async exchange() {
    exchanges++; const reply = { sequence: wrong === 'sequence' ? 2 : 1,
      reply: { workspaceBindingId: wrong === 'binding' ? 'foreign' : query.workspaceBindingId,
        operation: wrong === 'operation' ? 'native-session' : 'native-info', data: { status: 200, body: { pid: 42 } } },
      ...(wrong === 'extra' ? { arbitrary: true } : {}) };
    return bytes(reply);
  } }, async () => {});
  await assert.rejects(client.query(query, signal())); await assert.rejects(client.query(query, signal())); assert.equal(exchanges, 1);
});

test('client preserves caller signal, rechecks after exchange and excludes concurrent mutation/close', async () => {
  const entered = deferred(), release = deferred(), caller = signal(); let valid = true, exchanges = 0;
  const client = createNativeSessionWireClient(query.workspaceBindingId, { async exchange(_bytes, actual) {
    exchanges++; assert.equal(actual, caller); entered.release(); await release.promise;
    return bytes({ sequence: 1, reply: { workspaceBindingId: query.workspaceBindingId, operation: 'native-info', data: { status: 200, body: {} } } });
  } }, async actual => { assert.equal(actual, caller); assert.equal(valid, true, 'helper generation changed'); });
  const reading = client.query(query, caller); const rejected = assert.rejects(reading); await entered.promise;
  await assert.rejects(client.shutdown(caller)); valid = false; release.release(); await rejected;
  await assert.rejects(client.query(query, caller)); assert.equal(exchanges, 1);
});

test('handler rechecks identity after awaited output and never reports complete after replacement', async () => {
  let valid = true, writes = 0; const target = owner();
  const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => { assert.equal(valid, true); },
    async () => { writes++; valid = false; });
  await assert.rejects(handler.handle(bytes({ sequence: 1, query }), signal()));
  assert.equal(target.reads(), 1); assert.equal(writes, 1); assert.equal(handler.receipt().poisoned, true);
});

test('failed write and output overflow remain incomplete without a response replay', async () => {
  for (const mode of ['write', 'overflow'] as const) {
    let writes = 0;
    const target = { ...owner(), async query() { return { workspaceBindingId: query.workspaceBindingId, operation: 'native-info' as const,
      data: { status: 200, body: { text: mode === 'overflow' ? 'x'.repeat(NativeSessionLimits.reply) : 'saved' } } }; } };
    const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => {}, async () => {
      writes++; throw Error('uncertain fixed output write');
    });
    await assert.rejects(handler.handle(bytes({ sequence: 1, query }), signal()));
    await assert.rejects(handler.handle(bytes({ sequence: 2, query }), signal()));
    assert.equal(writes, mode === 'write' ? 1 : 0); assert.equal(handler.receipt().poisoned, true);
  }
});

test('oversized direct body and transport node/depth bounds deny before verify/query/write', async () => {
  for (const payload of [Buffer.alloc(NativeSessionLimits.request),
    bytes({ sequence: 1, query, extra: Array.from({ length: 50_000 }, () => 0) }),
    Buffer.from('['.repeat(65) + '0' + ']'.repeat(65))]) {
    const target = owner(); let verifies = 0, writes = 0;
    const handler = createNativeSessionWireHandler(query.workspaceBindingId, target, async () => { verifies++; }, async () => { writes++; });
    await assert.rejects(handler.handle(payload, signal())); assert.equal(verifies, 0); assert.equal(target.reads(), 0); assert.equal(writes, 0);
  }
});

test('client rejects unrequested shutdown and preaborted caller without a pipe effect', async () => {
  let exchanges = 0, verifies = 0;
  const client = createNativeSessionWireClient(query.workspaceBindingId, { async exchange() {
    exchanges++; return bytes({ sequence: 1, workspaceBindingId: query.workspaceBindingId, closed: true });
  } }, async () => { verifies++; });
  await assert.rejects(client.query(query, AbortSignal.abort())); assert.equal(exchanges, 0); assert.equal(verifies, 0);
  await assert.rejects(client.query(query, signal())); assert.equal(exchanges, 1); assert.equal(verifies, 1);
});

test('actual fixed pipe exchanges canonical reads through one captured SQLite descriptor, then closes before ack', async () => {
  const root = await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-wire-'));
  const callbacks: (() => Promise<void>)[] = [];
  try {
    const config = path.join(root, 'config'), repo = path.join(root, 'beta'), common = path.join(repo, '.git');
    for (const dir of [config, repo, common]) await fs.mkdir(dir);
    const stamp = async (path: string) => { const stat = await fs.lstat(path); return { path, device: stat.dev, inode: stat.ino }; };
    const configurationRoot = await stamp(config), filename = path.join(config, 'agents.db'), db = new DatabaseSync(filename);
    db.exec('CREATE TABLE actual_test_marker (value TEXT)'); db.close();
    const expected = await captureNativeStore(configurationRoot, () => {}, signal()); await expected.close();
    const service = path.join(config, 'agents-opencode/state/opencode'); await fs.mkdir(service, { recursive: true });
    await fs.writeFile(path.join(service, 'service.json'), JSON.stringify({ pid: 42, url: 'http://127.0.0.1:4123/', password: 'actual-test-private' }));
    let opens = 0, closes = 0, reads = 0;
    const files = { ...fs, async open(...args: Parameters<typeof fs.open>) {
      opens++; const actual = await fs.open(...args); const close = actual.close.bind(actual);
      actual.close = async () => { closes++; await close(); }; return actual;
    } };
    const repository = { repoName: 'beta', sourceRepoId: 'beta-source-id', repo, commonDir: common, groups: [] };
    const captured = await captureNativeQuerySession({ configurationRoot, workspaceBindingId: query.workspaceBindingId,
      workspace: { workspaceId: 'ACTUAL-WS', repo, storeId: expected.storeId, storeGeneration: expected.storeGeneration },
      sources: [{ sourceKey: 'beta-source', repository, root: await stamp(repo) }], commonDirectories: [await stamp(common)],
      pinnedExecutable: '/pinned/opencode', secrets: [], enrollCleanup: cleanup => callbacks.push(cleanup), verifyNamespace: async () => {},
      processIdentity: async pid => ({ pid, generation: 'actual-process', executable: '/pinned/opencode', argv: ['/pinned/opencode', 'serve', '--service'] }),
      fetch: async () => { reads++; return new Response(JSON.stringify({ pid: 42 })); },
      physical: { async source() { throw Error('not selected'); }, async agent() { throw Error('not selected'); } },
    }, signal(), files, guard);
    const child = new EventEmitter() as EventEmitter & { stdin: EventEmitter; stdout: PassThrough; stderr: PassThrough; exitCode: number | null; signalCode: null; killed: boolean; kill(): boolean };
    child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null; child.signalCode = null; child.killed = false;
    child.kill = () => { child.killed = true; return true; };
    let caller = signal();
    const handler = createNativeSessionWireHandler(query.workspaceBindingId, captured, async (actual, phase) => {
      assert.equal(actual, caller); if (phase === 'descriptors-closed') assert.equal(closes, 4);
    }, async reply => { child.stdout.write(reply); });
    child.stdin = Object.assign(new EventEmitter(), {
      write(frame: Buffer, callback: (error?: Error) => void) {
        const decoder = new NativeRequestFrame(); const body = decoder.push(frame)!; decoder.end(); callback();
        void handler.handle(body, caller).catch(() => child.emit('error', Error('fixed peer failure'))); return true;
      }, end() {},
    });
    const pipe = new NativeSessionPipe(cleanup => callbacks.push(cleanup), guard);
    pipe.start(signal(), () => child as unknown as ChildProcess);
    const client = createNativeSessionWireClient(query.workspaceBindingId, pipe, async (actual, phase) => {
      assert.equal(actual, caller); if (phase === 'descriptors-closed') assert.equal(closes, 4);
    });
    assert.deepEqual((await client.query(query, caller)).data, { status: 200, body: { pid: 42 } });
    caller = signal(); await client.query(query, caller); assert.equal(opens, 4); assert.equal(reads, 2); assert.equal(closes, 0);
    caller = signal(); await client.shutdown(caller); assert.equal(closes, 4);
    child.stdout.emit('end'); child.exitCode = 0; child.emit('exit', 0); child.emit('close', 0);
    assert.equal(pipe.receipt().poisoned, false); assert.equal(handler.receipt().poisoned, false);
    await pipe.dispose(); await captured.dispose(); assert.equal(opens, 4); assert.equal(closes, 4);
  } finally { for (const cleanup of callbacks.reverse()) await cleanup(); await fs.rm(root, { recursive: true, force: true }); }
});
