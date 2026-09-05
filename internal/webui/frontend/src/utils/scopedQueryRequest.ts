interface QueryOptions<T> {
  load: (signal: AbortSignal) => Promise<T>;
  commit: (data: T) => void;
  validateRecovery?: (data: T) => void;
  onError?: (error: Error) => void;
  onLoading?: (loading: boolean) => void;
}
interface Request {
  controller: AbortController;
  promise: Promise<void>;
  recovery: boolean;
}

/** One owner per committed query scope. Recovery supersedes pre-existing work;
 * ordinary refreshes join it. Cancellation fences even loaders ignoring abort. */
export class ScopedQueryRequest<T> {
  private active: Request | null = null;
  private generation = 0;
  constructor(private readonly options: QueryOptions<T>) {}

  run({
    signal,
    fresh = false,
  }: { signal?: AbortSignal; fresh?: boolean } = {}): Promise<void> {
    if (signal?.aborted) return Promise.reject(signal.reason);
    if (this.active && (!fresh || (this.active.recovery && !signal)))
      return this.active.promise;
    const generation = ++this.generation;
    this.cancelActive();
    if (generation !== this.generation)
      return Promise.reject(
        new DOMException("Query superseded while starting", "AbortError"),
      );
    const current: Request = {
      controller: new AbortController(),
      promise: Promise.resolve(),
      recovery: signal !== undefined,
    };
    this.active = current;
    const combined = signal
      ? AbortSignal.any([signal, current.controller.signal])
      : current.controller.signal;
    current.promise = Promise.resolve().then(() =>
      this.perform(current, combined),
    );
    return current.promise;
  }

  cancel(): void {
    this.generation++;
    this.cancelActive();
  }

  private cancelActive(): void {
    const current = this.active;
    if (!current) return;
    this.active = null;
    current.controller.abort(
      new DOMException("Query scope superseded", "AbortError"),
    );
    this.options.onLoading?.(false);
  }

  private assertCurrent(current: Request, signal: AbortSignal): void {
    signal.throwIfAborted();
    if (this.active !== current)
      throw new DOMException("Query scope superseded", "AbortError");
  }

  private async perform(current: Request, signal: AbortSignal): Promise<void> {
    let onAbort: () => void = () => {};
    try {
      this.assertCurrent(current, signal);
      this.options.onLoading?.(true);
      this.assertCurrent(current, signal);
      const aborted = new Promise<never>((_, reject) => {
        onAbort = () => reject(signal.reason);
        signal.addEventListener("abort", onAbort, { once: true });
        if (signal.aborted) onAbort();
      });
      const request = Promise.resolve().then(() => {
        this.assertCurrent(current, signal);
        return this.options.load(signal);
      });
      const data = await Promise.race([request, aborted]);
      this.assertCurrent(current, signal);
      if (current.recovery) this.options.validateRecovery?.(data);
      this.assertCurrent(current, signal);
      this.options.commit(data);
      this.assertCurrent(current, signal);
    } catch (error) {
      if (this.active === current && !signal.aborted) {
        this.options.onError?.(
          error instanceof Error ? error : new Error(String(error)),
        );
      }
      throw error;
    } finally {
      signal.removeEventListener("abort", onAbort);
      if (this.active === current) {
        this.active = null;
        this.options.onLoading?.(false);
      }
    }
  }
}
