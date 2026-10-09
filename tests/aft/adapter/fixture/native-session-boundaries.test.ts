import test from 'node:test';
import assert from 'node:assert/strict';
import { NativeReplyFrame, NativeSessionBudget, NativeSessionLimits,
  RetainedNativeSession, type NativeSessionGuard, type NativeSessionOwnedResource } from './native-session-boundaries.js';

const body = (value: unknown) => Buffer.from(JSON.stringify(value));
const frame = (payload: Uint8Array) => {
  const result = Buffer.alloc(payload.byteLength + 4);
  result.writeUInt32BE(payload.byteLength); result.set(payload, 4); return result;
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function manualGuard() {
  const guards: AbortController[] = [];
  const guard: NativeSessionGuard = { begin(signal) {
    const timer = new AbortController(); guards.push(timer);
    return { signal: AbortSignal.any([signal, timer.signal]), release() {} };
  } };
  return { guard, guards };
}
function resource(verify = async () => {}, dispose = async () => {}) {
  const value: NativeSessionOwnedResource = { verify, dispose }; return value;
}

test('length header and body may be fragmented; bytes and UTF8 content are exact', () => {
  const expected = body({ text: '😀\uFFFD\uFEFF', sequence: 1 });
  const bytes = frame(expected), decoder = new NativeReplyFrame();
  let value: Buffer | undefined;
  for (const byte of bytes) value = decoder.push(Buffer.from([byte]));
  assert.deepEqual(value, expected); decoder.end();
});

test('oversized/empty headers reject before body; partial EOF and extra frames are errors', () => {
  for (const size of [0, NativeSessionLimits.reply - 3, 0xffffffff]) {
    const header = Buffer.alloc(4); header.writeUInt32BE(size);
    assert.throws(() => new NativeReplyFrame().push(header));
  }
  const partial = new NativeReplyFrame(); partial.push(frame(body({})).subarray(0, 5));
  assert.throws(() => partial.end());
  const multiple = new NativeReplyFrame();
  assert.throws(() => multiple.push(Buffer.concat([frame(body({})), frame(body({}))])));
});

test('fatal UTF8 rejects byte-length-equivalent replacement and preserves genuine characters', () => {
  const malformed = Buffer.concat([Buffer.from('"'), Buffer.from([0xf0, 0x90, 0x80]), Buffer.from('"')]);
  assert.throws(() => new NativeReplyFrame().push(frame(malformed)));
  assert.throws(() => new NativeSessionBudget().request(malformed));
});

test('full framed reply bound is accepted; framing overhead is not free', () => {
  const maximum = body('x'.repeat(NativeSessionLimits.reply - 6));
  const decoder = new NativeReplyFrame();
  assert.deepEqual(decoder.push(frame(maximum)), maximum); decoder.end();
  assert.throws(() => new NativeReplyFrame().push(frame(Buffer.concat([maximum, Buffer.from(' ')]))));
});

test('transport depth/node ceilings reject rather than truncate', () => {
  const nested = (depth: number) => Buffer.from('['.repeat(depth) + '0' + ']'.repeat(depth));
  new NativeSessionBudget().request(nested(63));
  assert.throws(() => new NativeSessionBudget().request(nested(64)));
  const exact = body(Array.from({ length: 49_999 }, () => 0));
  new NativeSessionBudget().request(exact);
  assert.throws(() => new NativeSessionBudget().request(body(Array.from({ length: 50_000 }, () => 0))));
});

test('normal 127 frames reserve the 128th shutdown; no new traffic after shutdown', () => {
  const budget = new NativeSessionBudget();
  for (let i = 0; i < 127; i++) {
    budget.request(body({ sequence: i })); budget.stdout(frame(body({ sequence: i }))); budget.completeReply();
  }
  budget.request(body({ operation: 'close' }), true);
  budget.stdout(frame(body({ closed: true }))); budget.completeReply();
  assert.equal(budget.receipt().attemptedFrames, 128);
  assert.throws(() => budget.request(body({ sequence: 129 })));
  assert.equal(budget.receipt().poisoned, true);
});

test('normal traffic cannot use reserved input/output bytes; cleanup frame can', () => {
  const budget = new NativeSessionBudget();
  const request = body('x'.repeat(NativeSessionLimits.request - 6));
  const replyChunk = Buffer.alloc(NativeSessionLimits.reply);
  for (let i = 0; i < 15; i++) {
    budget.request(request); budget.stdout(replyChunk); budget.completeReply();
  }
  budget.request(request, true); budget.stdout(replyChunk); budget.completeReply();
  assert.equal(budget.receipt().outputBytes, NativeSessionLimits.output);
  const input = new NativeSessionBudget();
  for (let i = 0; i < 31; i++) { input.request(request); input.stdout(frame(body({}))); input.completeReply(); }
  input.request(request, true); input.stdout(frame(body({}))); input.completeReply();
  assert.equal(input.receipt().inputBytes, NativeSessionLimits.input);
});

test('overflow, overlap or unsolicited stdout poison session; counters never reset', () => {
  const budget = new NativeSessionBudget(); budget.request(body({}));
  assert.throws(() => budget.request(body({})));
  assert.throws(() => budget.request(body({}), true));
  assert.equal(budget.receipt().attemptedFrames, 1);
  const unsolicited = new NativeSessionBudget();
  assert.throws(() => unsolicited.stdout(Buffer.from('unsafe')));
  assert.equal(unsolicited.receipt().outputBytes, 6);
  assert.equal(unsolicited.receipt().poisoned, true);
});

test('normal input/output beyond reserved quota is terminal, with no implicit close replay', () => {
  const input = new NativeSessionBudget(), request = body('x'.repeat(NativeSessionLimits.request - 6));
  for (let i = 0; i < 31; i++) { input.request(request); input.stdout(frame(body({}))); input.completeReply(); }
  assert.throws(() => input.request(body({})));
  assert.equal(input.receipt().inputBytes, NativeSessionLimits.input - NativeSessionLimits.request);
  const output = new NativeSessionBudget(), chunk = Buffer.alloc(NativeSessionLimits.reply);
  for (let i = 0; i < 15; i++) { output.request(body({})); output.stdout(chunk); output.completeReply(); }
  output.request(body({})); assert.throws(() => output.stdout(Buffer.from('x')));
  assert.throws(() => output.request(body({ operation: 'close' }), true));
});

test('stderr is charged as raw UTF8 bytes across lifecycle, not per chunk or character', () => {
  const budget = new NativeSessionBudget();
  const fourBytes = Buffer.from('😀');
  for (let i = 0; i < NativeSessionLimits.stderr / 4; i++) budget.stderr(fourBytes);
  assert.equal(budget.receipt().stderrBytes, NativeSessionLimits.stderr);
  assert.throws(() => budget.stderr(Buffer.from('x')));
  assert.throws(() => budget.request(body({})));
});

test('cleanup enrolled before factory and concurrent pending acquisition cannot leak', async () => {
  const entered = deferred<void>(), opened = deferred<void>(); let closeCount = 0;
  let cleanup!: () => Promise<void>; const { guard } = manualGuard();
  const owner = new RetainedNativeSession(async (_signal, retain) => {
    assert.equal(typeof cleanup, 'function'); retain(resource(undefined, async () => { closeCount++; }));
    entered.resolve(); await opened.promise;
  }, callback => { cleanup = callback; }, guard);
  const acquiring = owner.acquire(new AbortController().signal);
  const denied = assert.rejects(acquiring);
  await entered.promise; const closing = cleanup(); opened.resolve();
  await Promise.all([denied, closing]); assert.equal(closeCount, 1);
  await cleanup(); assert.equal(closeCount, 1);
  await assert.rejects(owner.acquire(new AbortController().signal));
});

test('partial factory rejection retains exact prelaunch intent for cleanup', async () => {
  let closed = 0; let cleanup!: () => Promise<void>; const { guard } = manualGuard();
  const owner = new RetainedNativeSession(async (_signal, retain) => {
    retain(resource(undefined, async () => { closed++; })); throw new Error('launch uncertain');
  }, callback => { cleanup = callback; }, guard);
  await assert.rejects(owner.acquire(new AbortController().signal));
  await cleanup(); assert.equal(closed, 1);
});

test('startup timeout retains pending owner; cleanup deadline does not discard late resource', async () => {
  const entered = deferred<void>(), opened = deferred<void>(); let closeCount = 0;
  const { guard, guards } = manualGuard(); let cleanup!: () => Promise<void>;
  const owner = new RetainedNativeSession(async (_signal, retain) => {
    entered.resolve(); await opened.promise; retain(resource(undefined, async () => { closeCount++; }));
  }, callback => { cleanup = callback; }, guard);
  const acquiring = owner.acquire(new AbortController().signal);
  await entered.promise; const failed = assert.rejects(acquiring); guards[0]!.abort(); await failed;
  const closing = cleanup(); const incomplete = assert.rejects(closing); guards[1]!.abort(); await incomplete;
  opened.resolve(); await cleanup(); assert.equal(closeCount, 1);
});

test('verification must settle before disposal; timeout/failure close retains same resource for retry', async () => {
  const verifyEntered = deferred<void>(), verifyDone = deferred<void>(); let attempts = 0;
  const { guard } = manualGuard(); let cleanup!: () => Promise<void>;
  const owner = new RetainedNativeSession(async (_signal, retain) => retain(resource(async () => {
    verifyEntered.resolve(); await verifyDone.promise;
  }, async () => { if (++attempts === 1) throw new Error('close uncertain'); })), callback => { cleanup = callback; }, guard);
  const acquiring = owner.acquire(new AbortController().signal);
  const rejected = assert.rejects(acquiring); await verifyEntered.promise;
  const closing = cleanup(); const failed = assert.rejects(closing); assert.equal(attempts, 0);
  verifyDone.resolve(); await Promise.all([rejected, failed]);
  assert.equal(attempts, 1); await cleanup(); assert.equal(attempts, 2); await cleanup(); assert.equal(attempts, 2);
});

test('synchronous disposal before deferred factory dispatch has zero starts', async () => {
  let starts = 0; const { guard } = manualGuard();
  const owner = new RetainedNativeSession(async (_signal, retain) => { starts++; retain(resource()); }, () => {}, guard);
  const acquiring = owner.acquire(new AbortController().signal); const denied = assert.rejects(acquiring);
  await owner.dispose(); await denied; assert.equal(starts, 0);
});

test('concurrent close retries join one raw close; terminal closure remains monotonic after timeout', async () => {
  const entered = deferred<void>(), done = deferred<void>(); let closes = 0;
  const { guard, guards } = manualGuard(); let cleanup!: () => Promise<void>;
  const owner = new RetainedNativeSession(async (_signal, retain) => retain(resource(undefined, async () => {
    closes++; entered.resolve(); await done.promise;
  })), callback => { cleanup = callback; }, guard);
  await owner.acquire(new AbortController().signal);
  const first = cleanup(); await entered.promise;
  const failed = assert.rejects(first); guards[1]!.abort(); await failed;
  const second = cleanup(), third = cleanup(); assert.equal(closes, 1); done.resolve();
  await Promise.all([second, third]); await cleanup(); assert.equal(closes, 1);
});
