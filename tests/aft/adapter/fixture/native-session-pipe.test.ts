import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { ChildProcess } from 'node:child_process';
import { NativeSessionPipe } from './native-session-pipe.js';
import { NativeSessionLimits, type NativeSessionGuard } from './native-session-boundaries.js';

const payload = (value: unknown) => Buffer.from(JSON.stringify(value));
function framed(bytes: Uint8Array) {
  const result = Buffer.alloc(bytes.byteLength + 4); result.writeUInt32BE(bytes.byteLength); result.set(bytes, 4); return result;
}
class Input extends EventEmitter {
  writes: Buffer[] = [];
  callbacks: ((error?: Error | null) => void)[] = [];
  ends = 0;
  write(bytes: Buffer, callback: (error?: Error | null) => void) {
    this.writes.push(Buffer.from(bytes)); this.callbacks.push(callback); return false;
  }
  end() { this.ends++; }
  ack(error?: Error) { this.callbacks.shift()!(error); }
}
class Child extends EventEmitter {
  stdin = new Input(); stdout = new PassThrough(); stderr = new PassThrough();
  exitCode: number | null = null; signalCode = null;
  killed = false; kills = 0; killResult = true; killThrows = false;
  kill() { this.kills++; if (this.killThrows) throw new Error('private error'); this.killed = this.killResult; return this.killResult; }
  exit() { this.exitCode = 0; this.emit('exit', 0); }
  close() { this.exit(); this.emit('close', 0); }
}
function harness() {
  let cleanup!: () => Promise<void>; const timers: AbortController[] = [], child = new Child();
  const guard: NativeSessionGuard = { begin(signal) {
    const timer = new AbortController(); timers.push(timer);
    return { signal: AbortSignal.any([signal, timer.signal]), release() {} };
  } };
  const owner = new NativeSessionPipe(callback => { cleanup = callback; }, guard);
  const signal = () => new AbortController().signal;
  const start = () => owner.start(signal(), () => { assert.equal(typeof cleanup, 'function'); return child as unknown as ChildProcess; });
  const done = async () => { const closing = cleanup(); child.close(); await closing; };
  return { owner, child, timers, start, signal, cleanup: () => cleanup(), done };
}

test('fragmented reply and write acknowledgement both required; exact bytes counted without diagnostics', async () => {
  const h = harness(); h.start(); let settled = false;
  const bytes = payload({ text: '😀\uFFFD\uFEFF', sequence: 1 });
  const result = h.owner.exchange(payload({ sequence: 1 }), h.signal()).then(value => { settled = true; return value; });
  const frame = framed(bytes); for (const byte of frame) h.child.stdout.write(Buffer.from([byte]));
  h.child.stderr.write('private Bearer never-retained'); await Promise.resolve();
  assert.equal(settled, false); h.child.stdin.ack(); assert.deepEqual(await result, bytes);
  assert.deepEqual(h.child.stdin.writes[0], framed(payload({ sequence: 1 })));
  assert.equal(h.owner.receipt().outputBytes, frame.length); assert.equal(h.owner.receipt().stderrBytes, 29);
  assert.equal(JSON.stringify(h.owner.receipt()).includes('Bearer'), false); await h.done();
});

test('overlapping exchanges poison both attempts with only one write and no replay', async () => {
  const h = harness(); h.start(); const first = assert.rejects(h.owner.exchange(payload({ one: 1 }), h.signal()));
  await assert.rejects(h.owner.exchange(payload({ two: 2 }), h.signal())); await first;
  h.child.stdin.ack(new Error('late failed write')); assert.equal(h.child.stdin.writes.length, 1);
  await assert.rejects(h.owner.exchange(payload({ three: 3 }), h.signal())); await h.done();
});

for (const kind of ['extra', 'partial-eof', 'malformed-utf8', 'stream-error'] as const) test(`${kind} is incomplete, never a successful prefix`, async () => {
  const h = harness(); h.start(); const denied = assert.rejects(h.owner.exchange(payload({ request: 1 }), h.signal()));
  h.child.stdin.ack();
  if (kind === 'extra') h.child.stdout.write(Buffer.concat([framed(payload({ one: 1 })), framed(payload({ two: 2 }))]));
  if (kind === 'partial-eof') { h.child.stdout.write(Buffer.from([0, 0, 0])); h.child.stdout.emit('end'); }
  if (kind === 'malformed-utf8') h.child.stdout.write(framed(Buffer.from([34, 0xf0, 0x90, 0x80, 34])));
  if (kind === 'stream-error') h.child.stdout.emit('error', new Error('private stream error'));
  await denied; assert.equal(h.owner.receipt().poisoned, true); await h.done();
});

test('failed write after complete reply is still denied; an extra chunk before ack poisons pending reply', async () => {
  for (const error of ['write', 'extra'] as const) {
    const h = harness(); h.start(); const denied = assert.rejects(h.owner.exchange(payload({ one: 1 }), h.signal()));
    h.child.stdout.write(framed(payload({ done: true })));
    if (error === 'extra') h.child.stdout.write(framed(payload({ foreign: true })));
    h.child.stdin.ack(error === 'write' ? new Error('private failed write') : undefined);
    await denied; assert.equal(h.owner.receipt().poisoned, true); await h.done();
  }
});

test('idle stdout and lifetime stderr overflow fail before any request write', async () => {
  for (const stream of ['stdout', 'stderr'] as const) {
    const h = harness(); h.start();
    h.child[stream].write(stream === 'stdout' ? Buffer.from('x') : Buffer.alloc(NativeSessionLimits.stderr + 1));
    await assert.rejects(h.owner.exchange(payload({ query: 1 }), h.signal()));
    assert.equal(h.child.stdin.writes.length, 0); assert.equal(h.owner.receipt().poisoned, true); await h.done();
  }
});

test('guard expiry retains exact child and pending write; fresh cleanup retries failed termination without replay', async () => {
  const h = harness(); h.start(); h.child.killResult = false;
  const denied = assert.rejects(h.owner.exchange(payload({ query: 1 }), h.signal())); h.timers[0]!.abort(); await denied;
  const first = assert.rejects(h.cleanup()); h.timers.at(-1)!.abort(); await first; assert.equal(h.child.kills, 1);
  const second = h.cleanup(); assert.equal(h.child.kills, 2); assert.equal(h.child.stdin.ends, 1);
  h.child.close(); await second; h.child.stdin.ack(new Error('closed write'));
  await h.cleanup(); assert.equal(h.child.kills, 2); assert.equal(h.child.stdin.writes.length, 1);
});

test('cleanup aborts pending exchange, joins raw close, and never signals observed exit awaiting pipes', async () => {
  const h = harness(); h.start(); const denied = assert.rejects(h.owner.exchange(payload({ query: 1 }), h.signal()));
  h.child.exit(); const first = h.cleanup(), second = h.cleanup(); await denied;
  assert.equal(h.child.kills, 0); h.child.emit('close', 0); await Promise.all([first, second]);
  h.child.stdin.ack(new Error('late closed write')); assert.equal(h.child.stdin.ends, 1);
});

test('preaborted exchange has zero writes; failed/unstarted launch intents clean up without nonexistent close', async () => {
  const h = harness(); h.start(); const aborted = new AbortController(); aborted.abort();
  await assert.rejects(h.owner.exchange(payload({ query: 1 }), aborted.signal));
  assert.equal(h.child.stdin.writes.length, 0); assert.equal(h.owner.receipt().attemptedFrames, 0); await h.done();
  const failed = harness(); assert.throws(() => failed.owner.start(failed.signal(), () => { throw new Error('spawn failed'); }));
  await failed.cleanup(); const notStarted = harness(); await notStarted.cleanup(); let launched = false;
  assert.throws(() => notStarted.owner.start(notStarted.signal(), () => { launched = true; return new Child() as unknown as ChildProcess; }));
  assert.equal(launched, false);
});

test('synchronous start abort retains returned handle and never leaks a raw rejection', async () => {
  const h = harness(), caller = new AbortController();
  assert.throws(() => h.owner.start(caller.signal, () => { caller.abort(); return h.child as unknown as ChildProcess; }));
  const closing = h.cleanup(); assert.equal(h.child.kills, 1); h.child.close(); await closing;
});

test('an observed process exit denies a new frame while stream-close is still pending', async () => {
  const h = harness(); h.start(); h.child.exit();
  await assert.rejects(h.owner.exchange(payload({ query: 1 }), h.signal()));
  assert.equal(h.child.stdin.writes.length, 0); const closing = h.cleanup();
  assert.equal(h.child.kills, 0); h.child.emit('close', 0); await closing;
});

for (const source of ['stdin', 'stderr', 'child'] as const) test(`${source} error preserves uncertainty and denies any reply`, async () => {
  const h = harness(); h.start(); const denied = assert.rejects(h.owner.exchange(payload({ query: 1 }), h.signal()));
  (source === 'child' ? h.child : h.child[source]).emit('error', new Error('private error'));
  await denied; h.child.stdin.ack(new Error('failed write')); await h.done();
});

test('full framed bounds are accepted; oversized header and oversized request deny without prefix success', async () => {
  const h = harness(); h.start(); const request = payload('x'.repeat(NativeSessionLimits.request - 6));
  const body = payload('x'.repeat(NativeSessionLimits.reply - 6));
  const result = h.owner.exchange(request, h.signal()); h.child.stdin.ack(); h.child.stdout.write(framed(body));
  assert.deepEqual(await result, body); assert.equal(h.child.stdin.writes[0]!.length, NativeSessionLimits.request); await h.done();
  const oversized = harness(); oversized.start();
  await assert.rejects(oversized.owner.exchange(Buffer.concat([request, Buffer.from(' ')]), oversized.signal()));
  assert.equal(oversized.child.stdin.writes.length, 0); await oversized.done();
  const header = harness(); header.start(); const denied = assert.rejects(header.owner.exchange(payload({ query: 1 }), header.signal()));
  const bad = Buffer.alloc(4); bad.writeUInt32BE(NativeSessionLimits.reply - 3); header.child.stdin.ack(); header.child.stdout.write(bad);
  await denied; await header.done();
});

test('127 ordinary frames reserve shutdown; completed shutdown reply may end stream/child before ack', async () => {
  const h = harness(); h.start();
  for (let sequence = 0; sequence < 127; sequence++) {
    const result = h.owner.exchange(payload({ sequence }), h.signal()); h.child.stdin.ack();
    h.child.stdout.write(framed(payload({ sequence }))); await result;
  }
  const closing = h.owner.exchange(payload({ close: true }), h.signal(), true);
  h.child.stdout.write(framed(payload({ closed: true }))); h.child.stdout.emit('end'); h.child.close();
  h.child.stdin.ack(); assert.deepEqual(await closing, payload({ closed: true }));
  assert.equal(h.owner.receipt().attemptedFrames, 128);
  await assert.rejects(h.owner.exchange(payload({ next: true }), h.signal())); await h.cleanup();
});

test('nonzero close cannot certify a pending shutdown response even after a full reply', async () => {
  const h = harness(); h.start(); const denied = assert.rejects(h.owner.exchange(payload({ close: true }), h.signal(), true));
  h.child.stdout.write(framed(payload({ closed: true }))); h.child.emit('close', 7, null); h.child.stdin.ack();
  await denied; assert.equal(h.owner.receipt().poisoned, true); await h.cleanup();
});

test('acknowledged complete shutdown stays complete through later normal EOF and close', async () => {
  const h = harness(); h.start(); const closing = h.owner.exchange(payload({ close: true }), h.signal(), true);
  h.child.stdin.ack(); h.child.stdout.write(framed(payload({ closed: true })));
  assert.deepEqual(await closing, payload({ closed: true })); assert.equal(h.owner.receipt().poisoned, false);
  h.child.stdout.emit('end'); h.child.close();
  assert.equal(h.owner.receipt().poisoned, false); await h.cleanup(); assert.equal(h.child.kills, 0);
});

test('successful shutdown does not excuse late error, signal close, output or stderr overflow', async () => {
  for (const kind of ['nonzero', 'signal', 'extra', 'stderr'] as const) {
    const h = harness(); h.start(); const closing = h.owner.exchange(payload({ close: true }), h.signal(), true);
    h.child.stdin.ack(); h.child.stdout.write(framed(payload({ closed: true }))); await closing;
    if (kind === 'nonzero') h.child.emit('close', 7, null);
    if (kind === 'signal') h.child.emit('close', null, 'SIGTERM');
    if (kind === 'extra') h.child.stdout.write(framed(payload({ extra: true })));
    if (kind === 'stderr') h.child.stderr.write(Buffer.alloc(NativeSessionLimits.stderr + 1));
    assert.equal(h.owner.receipt().poisoned, true); await h.done();
  }
});
