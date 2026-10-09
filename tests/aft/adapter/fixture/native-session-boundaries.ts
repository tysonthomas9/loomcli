import { FixtureError } from './lifecycle.js';

/** Private byte boundaries only. The root-owned protocol still validates the
 * closed request/reply variant and its sequence/owner before these are used.
 * None of these counters establishes store, actor or process authority. */
export const NativeSessionLimits = Object.freeze({
  request: 1024 * 1024, reply: 4 * 1024 * 1024,
  nodes: 50_000, depth: 64, frames: 128,
  input: 32 * 1024 * 1024, output: 64 * 1024 * 1024,
  stderr: 64 * 1024, guardMs: 15_000,
});
const deny = (): never => { throw new FixtureError('observation-failed'); };
function checkJson(bytes: Uint8Array) {
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes)); }
  catch { deny(); }
  const pending: { value: unknown; depth: number }[] = [{ value, depth: 1 }];
  let nodes = 0;
  while (pending.length) {
    const entry = pending.pop()!;
    if (++nodes > NativeSessionLimits.nodes || entry.depth > NativeSessionLimits.depth) deny();
    if (entry.value !== null && typeof entry.value === 'object') {
      for (const child of Object.values(entry.value)) {
        // Keys consume the same transport work budget as values. This is a
        // conservative frame restriction, not a widened business JSON schema.
        if (!Array.isArray(entry.value) && ++nodes > NativeSessionLimits.nodes) deny();
        pending.push({ value: child, depth: entry.depth + 1 });
        if (nodes + pending.length > NativeSessionLimits.nodes) deny();
      }
    }
  }
}

/** Exactly one length-prefixed reply. Allocation follows the bounded header;
 * a second frame or trailing bytes are a protocol error, never a prefix. */
export class NativeReplyFrame {
  private readonly header = Buffer.alloc(4);
  private headerBytes = 0;
  private body?: Buffer;
  private bodyBytes = 0;
  private failed = false;
  private complete = false;
  push(chunk: Uint8Array): Buffer | undefined {
    try {
      if (this.failed || this.complete) deny();
      let offset = 0;
      if (this.headerBytes < 4) {
        const count = Math.min(4 - this.headerBytes, chunk.byteLength);
        this.header.set(chunk.subarray(0, count), this.headerBytes);
        this.headerBytes += count; offset += count;
        if (this.headerBytes < 4) return;
        const length = this.header.readUInt32BE();
        if (!length || length + 4 > NativeSessionLimits.reply) deny();
        this.body = Buffer.alloc(length);
      }
      const remaining = this.body!.length - this.bodyBytes;
      if (chunk.byteLength - offset > remaining) deny();
      this.body!.set(chunk.subarray(offset), this.bodyBytes);
      this.bodyBytes += chunk.byteLength - offset;
      if (this.bodyBytes !== this.body!.length) return;
      checkJson(this.body!); this.complete = true;
      return this.body;
    } catch (error) { this.failed = true; throw error; }
  }
  end() { if (this.failed || !this.complete) { this.failed = true; deny(); } }
}

/** Whole-session accounting includes framing, errors and close. Normal traffic
 * cannot spend the final frame, maximum request or maximum reply reserved for
 * shutdown. Poisoning never resets totals or permits another query. Owned
 * descriptor/process cleanup must remain available outside the wire protocol. */
export class NativeSessionBudget {
  private attempted = 0;
  private input = 0;
  private output = 0;
  private errorBytes = 0;
  private inFlight = false;
  private closing = false;
  private poisoned = false;
  request(payload: Uint8Array, shutdown = false): Buffer {
    try {
      if (this.poisoned || this.inFlight || this.closing) deny();
      this.attempted++;
      const bytes = payload.byteLength + 4;
      const limit = shutdown ? NativeSessionLimits.input : NativeSessionLimits.input - NativeSessionLimits.request;
      if (this.attempted > NativeSessionLimits.frames - (shutdown ? 0 : 1) ||
        bytes > NativeSessionLimits.request || this.input + bytes > limit) deny();
      checkJson(payload);
      const frame = Buffer.alloc(bytes); frame.writeUInt32BE(payload.byteLength); frame.set(payload, 4);
      this.input += bytes; this.inFlight = true; this.closing = shutdown;
      return frame;
    } catch (error) { this.poisoned = true; throw error; }
  }
  /** Called on EVERY stdout chunk before buffering/decoding, including startup
   * and failed reads. No uncharged idle-output buffer is permitted by a port. */
  stdout(chunk: Uint8Array) {
    this.output += chunk.byteLength;
    const limit = this.closing ? NativeSessionLimits.output : NativeSessionLimits.output - NativeSessionLimits.reply;
    if (this.poisoned || !this.inFlight || this.output > limit) { this.poisoned = true; deny(); }
  }
  stderr(chunk: Uint8Array) {
    this.errorBytes += chunk.byteLength;
    if (this.errorBytes > NativeSessionLimits.stderr) { this.poisoned = true; deny(); }
  }
  completeReply() {
    if (this.poisoned || !this.inFlight) deny();
    this.inFlight = false;
  }
  invalidate() { this.poisoned = true; }
  receipt() {
    return Object.freeze({ attemptedFrames: this.attempted, inputBytes: this.input,
      outputBytes: this.output, stderrBytes: this.errorBytes, poisoned: this.poisoned });
  }
}

export interface NativeSessionOwnedResource {
  verify(signal: AbortSignal): Promise<void>;
  /** Releases only the exact retained session/store/process. Implementations
   * must throw and retain their handles when close or exit is uncertain. */
  dispose(signal: AbortSignal): Promise<void>;
}
export interface NativeSessionGuard {
  begin(signal: AbortSignal): { signal: AbortSignal; release(): void };
}
export const nativeSessionGuard: NativeSessionGuard = {
  begin(signal) { return { signal: AbortSignal.any([signal, AbortSignal.timeout(NativeSessionLimits.guardMs)]), release() {} }; },
};
async function guarded<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
  // Work already exists and may have synchronously aborted the caller. Observe
  // its rejection even on the early exit; keep the original for cleanup joins.
  void work.catch(() => undefined);
  signal.throwIfAborted();
  let abort!: () => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    abort = () => reject(new FixtureError('observation-failed'));
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
  try { const result = await Promise.race([work, cancelled]); signal.throwIfAborted(); return result; }
  finally { signal.removeEventListener('abort', abort); }
}

/** Lifecycle envelope for an existing fixed session factory, not a launch API.
 * Authority/build checks precede construction by the trusted fixture owner.
 * Cleanup is enrolled before acquisition; pending raw promises remain retained
 * after a deadline, so a late process/descriptor cannot escape disposal. */
export class RetainedNativeSession {
  private opening?: Promise<void>;
  private resource?: NativeSessionOwnedResource;
  private pending?: Promise<void>;
  private disposing?: Promise<void>;
  private disposed = false;
  private disposalRequested = false;
  private readonly lifetime = new AbortController();
  constructor(private readonly factory: (signal: AbortSignal,
    retain: (resource: NativeSessionOwnedResource) => void) => Promise<void>,
    enrollCleanup: (cleanup: () => Promise<void>) => void,
    private readonly guard: NativeSessionGuard = nativeSessionGuard) {
    enrollCleanup(() => this.dispose());
  }
  async acquire(signal: AbortSignal) {
    signal.throwIfAborted();
    if (this.opening || this.disposalRequested) deny();
    const bound = this.guard.begin(AbortSignal.any([signal, this.lifetime.signal]));
    try {
      // Defer the factory until the owning intent/opening promise is stored.
      this.opening = Promise.resolve().then(() => {
        bound.signal.throwIfAborted();
        return this.factory(bound.signal, resource => {
          // The fixed factory retains its intent BEFORE launch/open. Partial
          // acquisition rejection must not discard the owning resource.
          if (this.resource) deny();
          this.resource = resource;
        });
      });
      await guarded(this.opening, bound.signal);
      const resource = this.resource ?? deny();
      this.pending = resource.verify(bound.signal);
      await guarded(this.pending, bound.signal);
      if (this.disposalRequested || this.resource !== resource) deny();
      bound.signal.throwIfAborted();
    } catch (error) { this.disposalRequested = true; this.lifetime.abort(); throw error; }
    finally { bound.release(); }
  }
  async dispose() {
    this.disposalRequested = true; this.lifetime.abort();
    if (this.disposed) return;
    const bound = this.guard.begin(new AbortController().signal);
    try {
      while (this.disposing) {
        try { await guarded(this.disposing, bound.signal); }
        catch { bound.signal.throwIfAborted(); }
        if (this.disposed) { bound.release(); return; }
      }
    } catch (error) { bound.release(); throw error; }
    if (this.disposed) { bound.release(); return; }
    const work = (async () => {
      if (this.opening) {
        try { await this.opening; }
        catch { /* The retained intent still owns partial acquisition. */ }
      }
      // Do not release a descriptor while a raw verification still uses it.
      if (this.pending) await this.pending.catch(() => undefined);
      bound.signal.throwIfAborted();
      if (this.resource) await this.resource.dispose(bound.signal);
      this.disposed = true;
    })();
    // Keep the raw cleanup promise after the public guard expires. A retry
    // waits for it instead of racing another close on the same descriptor.
    this.disposing = work.finally(() => { this.disposing = undefined; bound.release(); });
    return guarded(this.disposing, bound.signal);
  }
}
