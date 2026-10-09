import type { ChildProcess } from 'node:child_process';
import { FixtureError } from './lifecycle.js';
import { NativeReplyFrame, NativeSessionBudget, nativeSessionGuard,
  type NativeSessionGuard } from './native-session-boundaries.js';

const failure = () => new FixtureError('observation-failed');
interface Pending {
  frame: NativeReplyFrame;
  reply: Promise<Buffer>;
  resolve(bytes: Buffer): void;
  reject(error: Error): void;
  shutdown: boolean;
  complete: boolean;
}
/** Private framing/pipe resource ONLY. Its caller must use the root's closed
 * query/reply mapper and correlate the session/sequence/owner independently.
 * A proxy ChildProcess close does NOT prove an in-container helper/store exit.
 * No executable, argv, SQL, path, origin or credential is accepted by this port.
 * The fixed code-owned spawn callback is synchronous Node spawn semantics:
 * throwing means no returned child, never launch-then-throw or detached work. */
export class NativeSessionPipe {
  private readonly budget = new NativeSessionBudget();
  private readonly lifetime = new AbortController();
  private readonly closed: Promise<void>;
  private markClosed!: () => void;
  private child?: ChildProcess;
  private started = false;
  private exited = false;
  private exitObserved = false;
  private disposalRequested = false;
  private disposing?: Promise<void>;
  private pending?: Pending;
  constructor(enrollCleanup: (cleanup: () => Promise<void>) => void,
    private readonly guard: NativeSessionGuard = nativeSessionGuard) {
    this.closed = new Promise<void>(resolve => { this.markClosed = () => { this.exited = true; resolve(); }; });
    enrollCleanup(() => this.dispose());
  }
  private fail() {
    this.budget.invalidate(); this.pending?.reject(failure());
  }
  private terminate() {
    const child = this.child;
    if (!child || this.exitObserved || child.exitCode !== null || child.signalCode !== null || child.killed) return;
    try { child.kill(); } catch { /* Actual close is still required. */ }
  }
  private async wait<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
    // Retain the original and observe rejection even on synchronous preabort.
    void work.catch(() => undefined); signal.throwIfAborted();
    let abort!: () => void;
    const cancelled = new Promise<never>((_resolve, reject) => {
      abort = () => reject(failure()); signal.addEventListener('abort', abort, { once: true });
      if (signal.aborted) abort();
    });
    try { const result = await Promise.race([work, cancelled]); signal.throwIfAborted(); return result; }
    finally { signal.removeEventListener('abort', abort); }
  }
  start(signal: AbortSignal, spawnFixed: () => ChildProcess) {
    signal.throwIfAborted();
    if (this.started || this.disposalRequested) throw failure();
    this.started = true;
    try {
      this.child = spawnFixed();
      const child = this.child;
      child.once('exit', () => { this.exitObserved = true; });
      child.once('close', (code, signal) => {
        this.markClosed();
        if (code !== 0 || signal != null || !this.pending?.shutdown || !this.pending.complete) this.fail();
      });
      child.on('error', () => { this.fail(); });
      if (!child.stdin || !child.stdout || !child.stderr) throw failure();
      child.stdin.on('error', () => { this.fail(); });
      child.stdout.on('error', () => { this.fail(); });
      child.stderr.on('error', () => { this.fail(); });
      child.stdout.on('end', () => {
        if (!this.pending?.shutdown || !this.pending.complete) this.fail();
      });
      child.stdout.on('data', (chunk: Buffer) => {
        try {
          this.budget.stdout(chunk); // EVERY raw byte before decoding/allocation.
          const pending = this.pending;
          if (!pending) throw failure();
          const reply = pending.frame.push(chunk);
          if (reply) { pending.complete = true; pending.resolve(reply); }
        } catch { this.fail(); }
      });
      child.stderr.on('data', (chunk: Buffer) => {
        try { this.budget.stderr(chunk); } catch { this.fail(); }
        // Count, but never retain or return raw private diagnostic bytes.
      });
      signal.throwIfAborted();
    } catch {
      this.fail();
      if (!this.child) this.markClosed(); // Proven synchronous no-child attempt.
      throw failure();
    }
  }
  async exchange(payload: Uint8Array, signal: AbortSignal, shutdown = false): Promise<Buffer> {
    signal.throwIfAborted();
    if (!this.started || !this.child || this.exited || this.exitObserved ||
      this.child.exitCode !== null || this.child.signalCode !== null || this.child.killed ||
      this.disposalRequested || this.pending) {
      this.fail(); throw failure();
    }
    const bounded = this.guard.begin(AbortSignal.any([signal, this.lifetime.signal]));
    let resolve!: (value: Buffer) => void, reject!: (error: Error) => void;
    const reply = new Promise<Buffer>((yes, no) => { resolve = yes; reject = no; });
    void reply.catch(() => undefined);
    const pending: Pending = { frame: new NativeReplyFrame(), reply, resolve, reject, shutdown, complete: false };
    this.pending = pending;
    try {
      bounded.signal.throwIfAborted();
      const frame = this.budget.request(payload, shutdown);
      const written = new Promise<void>((yes, no) => {
        try { this.child!.stdin!.write(frame, error => error ? no(failure()) : yes()); }
        catch { no(failure()); }
      });
      // A reply cannot make a failed/uncertain write callback successful.
      const work = Promise.all([reply, written]);
      const [bytes] = await this.wait(work, bounded.signal);
      bounded.signal.throwIfAborted();
      pending.frame.end(); this.budget.completeReply();
      return bytes;
    } catch {
      this.fail(); throw failure();
    } finally {
      if (this.pending === pending) this.pending = undefined;
      bounded.release();
    }
  }
  receipt() { return this.budget.receipt(); }
  async dispose(): Promise<void> {
    this.disposalRequested = true; this.lifetime.abort(); this.fail();
    if (!this.started) { this.started = true; this.markClosed(); return; }
    if (this.exited) return;
    const bounded = this.guard.begin(new AbortController().signal);
    try {
      if (!this.disposing) {
        const work = (async () => {
          // Fixed owned direct-child cleanup, never a PID/name/port lookup.
          try { this.child?.stdin?.end(); } catch { /* Retain until close. */ }
          this.terminate(); await this.closed;
        })();
        this.disposing = work.finally(() => { this.disposing = undefined; });
      } else this.terminate(); // Retry only the same uncertain direct child.
      await this.wait(this.disposing, bounded.signal);
    } finally { bounded.release(); }
  }
}
