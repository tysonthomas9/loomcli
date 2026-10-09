import type { Readable, Writable } from 'node:stream';
import { Id } from '../protocol.js';
import { FixtureError } from './lifecycle.js';
import { NativeSessionLimits, nativeSessionGuard, type NativeSessionGuard } from './native-session-boundaries.js';
import { NativeRequestFrame, createNativeSessionWireHandler, type NativeSessionWireVerify } from './native-session-wire.js';
import type { captureNativeQuerySession } from './native-session.js';

type QueryOwner = Awaited<ReturnType<typeof captureNativeQuerySession>>;
type FixedFactory = (signal: AbortSignal, enrollCleanup: (cleanup: () => Promise<void>) => void) => Promise<QueryOwner>;
const failure = () => new FixtureError('observation-failed');
async function wait<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
  void work.catch(() => undefined); signal.throwIfAborted();
  let abort!: () => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    abort = () => reject(failure()); signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
  try { const value = await Promise.race([work, cancelled]); signal.throwIfAborted(); return value; }
  finally { signal.removeEventListener('abort', abort); }
}

/** Fixed helper stdin/stdout resource. The launcher supplies its exact owned
 * streams and query factory AFTER authority/build/kernel/namespace preflight.
 * This does not launch, infer a process exit, or map host paths into a container.
 * The factory must enroll the ONE retained query-owner cleanup before opening.
 * Node stream close and the helper's actual kernel exit remain distinct. */
export class NativeSessionStream {
  private readonly lifetime = new AbortController();
  private opening?: Promise<QueryOwner>;
  private owner?: QueryOwner;
  private reading?: Promise<IteratorResult<unknown>>;
  private handling?: Promise<void>;
  private cleanup?: () => Promise<void>;
  private cleanupWork?: Promise<void>;
  private cleanupSucceeded = false;
  private cleanupFailed = false;
  private disposing?: Promise<void>;
  private inputClose?: Promise<void>;
  private outputClose?: Promise<void>;
  private started = false;
  private terminal = false;
  private disposed = false;
  private completed = false;
  private descriptorsClosed = false;
  private rawInputBytes = 0;
  private wire?: ReturnType<typeof createNativeSessionWireHandler>;
  constructor(private readonly input: Readable, private readonly output: Writable,
    enrollCleanup: (cleanup: () => Promise<void>) => void,
    private readonly guard: NativeSessionGuard = nativeSessionGuard) {
    // Intent is visible before listeners, any read, or the fixed factory.
    enrollCleanup(() => this.dispose());
    input.on('error', () => { this.lifetime.abort(); });
    output.on('error', () => { this.lifetime.abort(); });
    output.on('close', () => { if (!this.completed) this.lifetime.abort(); });
  }
  private startCleanup() {
    if (!this.cleanup || this.cleanupSucceeded || (this.cleanupWork && !this.cleanupFailed)) return;
    this.cleanupFailed = false;
    const work = Promise.resolve().then(() => this.cleanup!());
    void work.catch(() => undefined);
    this.cleanupWork = work.then(() => { this.cleanupSucceeded = true; }, error => { this.cleanupFailed = true; throw error; });
    void this.cleanupWork.catch(() => undefined);
  }
  private closeStream(stream: Readable | Writable): Promise<void> {
    if (stream.closed) return Promise.resolve();
    const closed = new Promise<void>(resolve => stream.once('close', resolve));
    stream.destroy(); return closed;
  }
  async run(workspaceBindingId: string, factory: FixedFactory, verify: NativeSessionWireVerify, signal: AbortSignal) {
    signal.throwIfAborted(); Id.parse(workspaceBindingId); if (this.started || this.terminal) throw failure();
    this.started = true;
    const startup = this.guard.begin(AbortSignal.any([signal, this.lifetime.signal]));
    let startupReleased = false;
    let frameGuard: ReturnType<NativeSessionGuard['begin']> | undefined;
    try {
      // Raw startup work is stored/observed before a synchronous abort can exit.
      this.opening = Promise.resolve().then(async () => {
        startup.signal.throwIfAborted(); await verify(signal, 'active'); signal.throwIfAborted();
        if (this.terminal) throw failure();
        const owner = await factory(signal, cleanup => {
          if (this.cleanup) throw failure();
          this.cleanup = cleanup;
          if (this.terminal) this.startCleanup();
        });
        this.owner = owner;
        if (!this.cleanup) { this.cleanup = owner.dispose.bind(owner); throw failure(); }
        if (this.terminal) throw failure();
        return owner;
      });
      await wait(this.opening, startup.signal); startup.release(); startupReleased = true;
      const owner = this.owner!;
      this.wire = createNativeSessionWireHandler(workspaceBindingId, {
        query: owner.query.bind(owner),
        dispose: async () => { await owner.dispose(); this.descriptorsClosed = true; },
      }, verify, (frame, frameSignal) => {
        frameSignal.throwIfAborted();
        const written = new Promise<void>((resolve, reject) => {
          try { this.output.write(frame, error => error ? reject(failure()) : resolve()); }
          catch { reject(failure()); }
        });
        void written.catch(() => undefined); return written;
      });
      const iterator = this.input[Symbol.asyncIterator]();
      let decoder = new NativeRequestFrame();
      const caller = AbortSignal.any([signal, this.lifetime.signal]);
      for (;;) {
        this.reading = iterator.next();
        const next = await wait(this.reading, frameGuard?.signal ?? caller);
        if (next.done) throw failure(); // EOF without a completed shutdown is incomplete.
        if (!(next.value instanceof Uint8Array)) throw failure();
        this.rawInputBytes += next.value.byteLength;
        if (this.rawInputBytes > NativeSessionLimits.input || this.terminal) throw failure();
        // One timer covers the whole frame from its first observed raw chunk;
        // fragments never reset it. Idle between frames is lease-owned.
        frameGuard ??= this.guard.begin(caller);
        frameGuard.signal.throwIfAborted();
        const payload = decoder.push(next.value);
        if (!payload) continue;
        decoder.end();
        this.handling = this.wire.handle(payload, frameGuard.signal);
        await wait(this.handling, frameGuard.signal);
        frameGuard.release(); frameGuard = undefined;
        if (this.descriptorsClosed) {
          // No further command can use the disposed descriptor authority.
          this.completed = true; this.terminal = true;
          return this.receipt();
        }
        decoder = new NativeRequestFrame();
      }
    } catch (error) { this.terminal = true; this.lifetime.abort(); throw error; }
    finally { frameGuard?.release(); if (!startupReleased) startup.release(); }
  }
  receipt() {
    return Object.freeze({ rawInputBytes: this.rawInputBytes, completed: this.completed, descriptorsClosed: this.descriptorsClosed,
      wire: this.wire?.receipt() ?? null });
  }
  async dispose() {
    this.terminal = true; this.lifetime.abort();
    if (this.disposed) return;
    const bound = this.guard.begin(new AbortController().signal);
    try {
      while (this.disposing) {
        try { await wait(this.disposing, bound.signal); }
        catch { bound.signal.throwIfAborted(); }
        if (this.disposed) return;
      }
      if (!this.disposing) {
        const work = (async () => {
          // Destruction can settle pending Node reads/writes, but does not
          // release their reservations or stand in for actual helper exit.
          this.inputClose ??= this.closeStream(this.input);
          this.outputClose ??= this.closeStream(this.output);
          this.startCleanup(); // Make a pending query capture terminal NOW.
          if (this.opening) await this.opening.catch(() => undefined);
          if (this.reading) await this.reading.catch(() => undefined);
          if (this.handling) await this.handling.catch(() => undefined);
          bound.signal.throwIfAborted();
          if (this.cleanupWork) {
            try { await this.cleanupWork; }
            finally { this.cleanupWork = undefined; }
          }
          await Promise.all([this.inputClose, this.outputClose]); bound.signal.throwIfAborted();
          this.disposed = true;
        })();
        this.disposing = work.finally(() => { this.disposing = undefined; });
        void this.disposing.catch(() => undefined);
      }
      return await wait(this.disposing, bound.signal);
    } finally { bound.release(); }
  }
}
