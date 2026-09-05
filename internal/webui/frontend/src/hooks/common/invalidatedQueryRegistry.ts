/**
 * Shared invalidated-query registry. It guarantees one debounced fetch, poll
 * timer, visibility listener, and request state per equal key. Events with a
 * non-empty entity_type match the configured entity/action filters first (a
 * caller with only `types` matches them by coarse type); the global `refresh`
 * fallback applies only when entity_type is absent. Connection epochs
 * invalidate after completed handshakes, and equal keys share one entry.
 */

import { createContext } from "react";

import type {
  MutationEntityType,
  MutationPayload,
  MutationType,
} from "@/types/workspace/mutation";

export interface UseInvalidatedQueryOptions {
  key: string;
  enabled?: boolean;
  entityTypes?: MutationEntityType[];
  actions?: string[];
  types?: MutationType[];
  debounceMs?: number;
  safetyPollMs?: number;
  pauseWhenHidden?: boolean;
  refetchOnConnect?: boolean;
  resetOnKeyChange?: boolean;
}

export interface UseInvalidatedQuerySnapshot<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
}

type Fetcher<T> = (signal: AbortSignal) => Promise<T>;
type Listener = () => void;

interface NormalizedOptions {
  entityTypes: MutationEntityType[] | undefined;
  actions: string[] | undefined;
  types: MutationType[] | undefined;
  debounceMs: number;
  safetyPollMs: number;
  pauseWhenHidden: boolean;
  refetchOnConnect: boolean;
}

interface Registration<T> {
  id: number;
  fetcherRef: { current: Fetcher<T> | undefined };
  committed: boolean;
  enabled: boolean;
}

interface InFlight {
  controller: AbortController;
  generation: number;
  token: object;
}

const DEFAULT_OPTIONS: Omit<
  NormalizedOptions,
  "entityTypes" | "actions" | "types"
> = {
  debounceMs: 200,
  safetyPollMs: 0,
  pauseWhenHidden: true,
  refetchOnConnect: true,
};

function normalizeOptions(
  options: UseInvalidatedQueryOptions,
): NormalizedOptions {
  return {
    entityTypes:
      options.entityTypes && options.entityTypes.length > 0
        ? [...options.entityTypes]
        : undefined,
    actions:
      options.actions && options.actions.length > 0
        ? [...options.actions]
        : undefined,
    types:
      options.types && options.types.length > 0
        ? [...options.types]
        : undefined,
    debounceMs: Math.max(0, options.debounceMs ?? DEFAULT_OPTIONS.debounceMs),
    safetyPollMs: Math.max(
      0,
      options.safetyPollMs ?? DEFAULT_OPTIONS.safetyPollMs,
    ),
    pauseWhenHidden: options.pauseWhenHidden ?? DEFAULT_OPTIONS.pauseWhenHidden,
    refetchOnConnect:
      options.refetchOnConnect ?? DEFAULT_OPTIONS.refetchOnConnect,
  };
}

function arrayKey(values: string[] | undefined): string {
  return values ? [...values].sort().join("\u0000") : "";
}

function optionsMismatch(a: NormalizedOptions, b: NormalizedOptions): boolean {
  return (
    arrayKey(a.entityTypes) !== arrayKey(b.entityTypes) ||
    arrayKey(a.actions) !== arrayKey(b.actions) ||
    arrayKey(a.types) !== arrayKey(b.types) ||
    a.debounceMs !== b.debounceMs ||
    a.safetyPollMs !== b.safetyPollMs ||
    a.pauseWhenHidden !== b.pauseWhenHidden ||
    a.refetchOnConnect !== b.refetchOnConnect
  );
}

function isAbortError(error: unknown): boolean {
  return (
    (error instanceof DOMException && error.name === "AbortError") ||
    (error instanceof Error && error.name === "AbortError")
  );
}

class InvalidatedQueryEntry<T> {
  readonly key: string;
  readonly options: NormalizedOptions;
  readonly registrations = new Map<number, Registration<T>>();
  readonly listeners = new Set<Listener>();

  private snapshot: UseInvalidatedQuerySnapshot<T> = {
    data: null,
    loading: false,
    error: null,
  };
  private nextRegistrationId = 0;
  private enabledCount = 0;
  private generation = 0;
  private latestEpoch: number;
  private fetchEpoch: number;
  private inFlight: InFlight | null = null;
  private debounceTimer: ReturnType<typeof setTimeout> | null = null;
  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private visibilityListener: (() => void) | null = null;
  private dirty = false;
  private trailing = false;
  private trailingForce = false;
  private pendingRefetches: Array<() => void> = [];
  private recovery: {
    request: InFlight | null;
    settle: (error?: unknown) => void;
  } | null = null;

  recoveryIdentity(): string {
    return [...this.registrations.values()]
      .filter((registration) => registration.committed && registration.enabled)
      .map((registration) => registration.id)
      .join(",");
  }

  refreshForRecovery(signal: AbortSignal): Promise<void> {
    if (signal.aborted) return Promise.reject(recoveryAbort());
    this.recovery?.settle(recoveryAbort());
    // An older request cannot certify reads begun after the recovery fence.
    this.generation += 1;
    this.inFlight?.controller.abort();
    this.inFlight = null;
    this.clearDebounce();
    this.trailing = false;
    this.trailingForce = false;
    return new Promise<void>((resolve, reject) => {
      const recovery = {
        request: null as InFlight | null,
        settle: (error?: unknown) => {
          if (this.recovery !== recovery) return;
          this.recovery = null;
          signal.removeEventListener("abort", abort);
          if (error === undefined) resolve();
          else reject(error);
        },
      };
      const abort = () => {
        if (this.recovery !== recovery) return;
        recovery.request?.controller.abort();
        if (this.inFlight === recovery.request) {
          this.inFlight = null;
          this.generation += 1;
          this.setSnapshot({ loading: false });
        }
        recovery.settle(recoveryAbort());
        this.settlePendingRefetches();
      };
      this.recovery = recovery;
      signal.addEventListener("abort", abort, { once: true });
      this.startFetch(true);
      if (!recovery.request) {
        recovery.settle(
          new Error(`No committed enabled fetcher for ${this.key}`),
        );
      }
    });
  }

  constructor(
    key: string,
    options: NormalizedOptions,
    connectionEpoch: number,
  ) {
    this.key = key;
    this.options = options;
    this.latestEpoch = connectionEpoch;
    this.fetchEpoch = connectionEpoch;
  }

  getSnapshot = (): UseInvalidatedQuerySnapshot<T> => this.snapshot;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  register(fetcher: Fetcher<T>): Registration<T> {
    const registration: Registration<T> = {
      id: ++this.nextRegistrationId,
      fetcherRef: { current: undefined },
      committed: false,
      enabled: false,
    };
    registration.fetcherRef.current = fetcher;
    this.registrations.set(registration.id, registration);
    return registration;
  }

  commit(registration: Registration<T>, fetcher: Fetcher<T>): void {
    if (!this.registrations.has(registration.id)) return;
    registration.fetcherRef.current = fetcher;
    registration.committed = true;
  }

  setEnabled(registration: Registration<T>, enabled: boolean): void {
    if (
      !this.registrations.has(registration.id) ||
      registration.enabled === enabled
    ) {
      return;
    }
    registration.enabled = enabled;
    this.enabledCount += enabled ? 1 : -1;
    if (enabled && this.enabledCount === 1) {
      this.activate();
    } else if (!enabled && this.enabledCount === 0) {
      this.deactivate();
    }
  }

  unregister(registration: Registration<T>): boolean {
    if (!this.registrations.delete(registration.id)) return false;
    if (registration.enabled) {
      registration.enabled = false;
      this.enabledCount -= 1;
      if (this.enabledCount === 0) this.deactivate();
    }
    return this.registrations.size === 0;
  }

  onEpoch(epoch: number): void {
    this.latestEpoch = epoch;
    if (
      this.options.refetchOnConnect &&
      this.enabledCount > 0 &&
      epoch !== this.fetchEpoch
    ) {
      this.invalidateNow();
    }
  }

  invalidate(mutation: MutationPayload): void {
    if (this.enabledCount === 0 || !this.matches(mutation)) return;
    this.invalidateNow();
  }

  refetch(): Promise<void> {
    this.clearDebounce();
    if (this.inFlight) {
      this.trailing = true;
      this.trailingForce = true;
      return new Promise<void>((resolve) => {
        this.pendingRefetches.push(resolve);
      });
    }
    return new Promise<void>((resolve) => {
      this.pendingRefetches.push(resolve);
      this.startFetch();
      if (!this.inFlight) this.settlePendingRefetches();
    });
  }

  destroy(): void {
    this.listeners.clear();
    this.deactivate();
    this.generation += 1;
    this.settlePendingRefetches();
  }

  private activate(): void {
    this.installVisibilityListener();
    if (this.options.safetyPollMs > 0) {
      this.pollTimer = setInterval(
        () => this.pollTick(),
        this.options.safetyPollMs,
      );
    }
    this.startFetch();
  }

  private deactivate(): void {
    this.recovery?.settle(recoveryAbort());
    this.generation += 1;
    if (this.inFlight) this.inFlight.controller.abort();
    this.inFlight = null;
    this.clearDebounce();
    if (this.pollTimer !== null) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    if (this.visibilityListener) {
      document.removeEventListener("visibilitychange", this.visibilityListener);
      this.visibilityListener = null;
    }
    this.dirty = false;
    this.trailing = false;
    this.trailingForce = false;
    if (this.snapshot.loading) this.setSnapshot({ loading: false });
    this.settlePendingRefetches();
  }

  private installVisibilityListener(): void {
    if (
      !this.options.pauseWhenHidden ||
      typeof document === "undefined" ||
      this.visibilityListener
    ) {
      return;
    }
    this.visibilityListener = () => {
      if (document.visibilityState !== "visible" || this.enabledCount === 0) {
        return;
      }
      const wasDirty = this.dirty;
      this.dirty = false;
      this.clearDebounce();
      if (this.inFlight) {
        this.trailing = true;
      } else if (wasDirty || document.visibilityState === "visible") {
        this.startFetch();
      }
    };
    document.addEventListener("visibilitychange", this.visibilityListener);
  }

  private pollTick(): void {
    if (this.enabledCount === 0) return;
    if (this.isHidden()) {
      this.dirty = true;
      return;
    }
    if (this.inFlight) {
      this.trailing = true;
    } else {
      this.startFetch();
    }
  }

  private invalidateNow(): void {
    if (this.enabledCount === 0) return;
    if (this.isHidden()) {
      this.dirty = true;
      return;
    }
    if (this.inFlight) {
      this.trailing = true;
      return;
    }
    this.clearDebounce();
    this.debounceTimer = setTimeout(() => {
      this.debounceTimer = null;
      if (this.enabledCount === 0) return;
      if (this.isHidden()) return;
      if (this.inFlight) {
        this.trailing = true;
      } else {
        this.startFetch();
      }
    }, this.options.debounceMs);
  }

  private clearDebounce(): void {
    if (this.debounceTimer !== null) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
  }

  private isHidden(): boolean {
    return (
      this.options.pauseWhenHidden &&
      typeof document !== "undefined" &&
      document.visibilityState === "hidden"
    );
  }

  private findFetcher(enabledOnly = false): Fetcher<T> | undefined {
    for (const registration of this.registrations.values()) {
      if (
        registration.committed &&
        (!enabledOnly || registration.enabled) &&
        registration.fetcherRef.current
      ) {
        return registration.fetcherRef.current;
      }
    }
    return undefined;
  }

  private startFetch(enabledOnly = false): void {
    if (this.inFlight) return;
    const fetcher = this.findFetcher(enabledOnly);
    if (!fetcher) return;

    const controller = new AbortController();
    const token = {};
    const current: InFlight = {
      controller,
      generation: this.generation,
      token,
    };
    this.inFlight = current;
    if (enabledOnly && this.recovery) this.recovery.request = current;
    this.fetchEpoch = this.latestEpoch;
    this.dirty = false;
    this.setSnapshot({ loading: true });

    if (controller.signal.aborted) return;
    let request: Promise<T>;
    try {
      request = fetcher(controller.signal);
    } catch (error) {
      request = Promise.reject(error);
    }
    void request.then(
      (data) => this.finishFetch(current, data, null),
      (error: unknown) =>
        this.finishFetch(
          current,
          null,
          error ?? new Error("Query fetch rejected without an error"),
        ),
    );
  }

  private finishFetch(current: InFlight, data: T | null, error: unknown): void {
    if (this.inFlight !== current || current.generation !== this.generation) {
      return;
    }
    this.inFlight = null;
    const aborted = isAbortError(error);
    if (!aborted && error === null) {
      this.setSnapshot({ data, error: null, loading: false });
    } else if (!aborted) {
      this.setSnapshot({
        error: error instanceof Error ? error : new Error(String(error)),
        loading: false,
      });
    } else {
      this.setSnapshot({ loading: false });
    }

    if (this.recovery?.request === current) {
      this.recovery.settle(error === null ? undefined : error);
    }

    const shouldTrail = this.trailing;
    const forceTrail = this.trailingForce;
    this.trailing = false;
    this.trailingForce = false;
    if (shouldTrail) {
      if (
        (this.enabledCount > 0 || forceTrail) &&
        (!this.isHidden() || forceTrail)
      ) {
        this.startFetch();
        return;
      }
      if (this.isHidden()) this.dirty = true;
    }
    this.settlePendingRefetches();
  }

  private settlePendingRefetches(): void {
    const pending = this.pendingRefetches.splice(0);
    for (const resolve of pending) resolve();
  }

  private setSnapshot(patch: Partial<UseInvalidatedQuerySnapshot<T>>): void {
    const next = {
      data: patch.data === undefined ? this.snapshot.data : patch.data,
      loading:
        patch.loading === undefined ? this.snapshot.loading : patch.loading,
      error: patch.error === undefined ? this.snapshot.error : patch.error,
    };
    if (
      next.data === this.snapshot.data &&
      next.loading === this.snapshot.loading &&
      next.error === this.snapshot.error
    ) {
      return;
    }
    this.snapshot = next;
    for (const listener of this.listeners) listener();
  }

  private matches(mutation: MutationPayload): boolean {
    const entityType = mutation.entity_type;
    if (entityType !== undefined && entityType !== null && entityType !== "") {
      if (this.options.entityTypes === undefined) {
        // Caller filters only by legacy type: an entity-typed event still has
        // to match that coarse type, and `refresh` never matches here because
        // entity-scoped refreshes (agent.refresh) are not global.
        return this.options.types?.includes(mutation.type) ?? false;
      }
      return (
        this.options.entityTypes.includes(entityType) &&
        (this.options.actions === undefined ||
          (mutation.action !== undefined &&
            this.options.actions.includes(mutation.action)))
      );
    }
    return (
      mutation.type === "refresh" ||
      (this.options.types?.includes(mutation.type) ?? false)
    );
  }
}

export interface InvalidatedQueryRegistration<T> {
  readonly key: string;
  getSnapshot(): UseInvalidatedQuerySnapshot<T>;
  subscribe(listener: Listener): () => void;
  commit(fetcher: Fetcher<T>): void;
  revive(fetcher: Fetcher<T>, connectionEpoch: number): void;
  setEnabled(enabled: boolean): void;
  onEpoch(epoch: number): void;
  invalidate(mutation: MutationPayload): void;
  refetch(): Promise<void>;
  dispose(): void;
}

function recoveryAbort(): DOMException {
  return new DOMException(
    "Query recovery canceled or superseded",
    "AbortError",
  );
}

interface RegistryRecovery {
  signal: AbortSignal;
  members: Map<
    InvalidatedQueryEntry<unknown>,
    {
      identity: string;
      controller: AbortController;
      done: boolean;
    }
  >;
  finish: (error?: unknown) => void;
}

export class InvalidatedQueryRegistry {
  private readonly entries = new Map<string, InvalidatedQueryEntry<unknown>>();
  private recovery: RegistryRecovery | null = null;
  private recoveryRevision = 0;
  private requiredIdentities = new Map<
    InvalidatedQueryEntry<unknown>,
    string
  >();

  /** Changes when the committed enabled query membership changes, even idle. */
  getRecoveryRevision(): number {
    return this.recoveryRevision;
  }

  private updateRecoveryRevision(): void {
    const next = new Map<InvalidatedQueryEntry<unknown>, string>();
    for (const entry of this.entries.values()) {
      const identity = entry.recoveryIdentity();
      if (identity) next.set(entry, identity);
    }
    if (
      next.size !== this.requiredIdentities.size ||
      [...next].some(
        ([entry, identity]) => this.requiredIdentities.get(entry) !== identity,
      )
    ) {
      this.requiredIdentities = next;
      this.recoveryRevision += 1;
    }
  }

  /** Resolve only after fresh successful reads for every active query identity. */
  refreshForRecovery(signal: AbortSignal): Promise<void> {
    this.recovery?.finish(recoveryAbort());
    if (signal.aborted) return Promise.reject(recoveryAbort());
    return new Promise<void>((resolve, reject) => {
      const attempt: RegistryRecovery = {
        signal,
        members: new Map(),
        finish: (error?: unknown) => {
          if (this.recovery !== attempt) return;
          this.recovery = null;
          signal.removeEventListener("abort", abort);
          for (const member of attempt.members.values())
            member.controller.abort();
          if (error === undefined) resolve();
          else reject(error);
        },
      };
      const abort = () => attempt.finish(recoveryAbort());
      this.recovery = attempt;
      signal.addEventListener("abort", abort, { once: true });
      this.syncRecovery();
    });
  }

  private syncRecovery(): void {
    this.updateRecoveryRevision();
    const attempt = this.recovery;
    if (!attempt) return;
    const active = new Set(this.entries.values());
    for (const [entry, member] of attempt.members) {
      if (!active.has(entry) || entry.recoveryIdentity() !== member.identity) {
        attempt.members.delete(entry);
        member.controller.abort();
      }
    }
    for (const entry of active) {
      const identity = entry.recoveryIdentity();
      if (!identity || attempt.members.has(entry)) continue;
      const member = {
        identity,
        controller: new AbortController(),
        done: false,
      };
      attempt.members.set(entry, member);
      void entry.refreshForRecovery(member.controller.signal).then(
        () => {
          if (
            this.recovery !== attempt ||
            attempt.members.get(entry) !== member
          )
            return;
          member.done = true;
          this.syncRecovery();
        },
        (error: unknown) => {
          if (
            this.recovery === attempt &&
            attempt.members.get(entry) === member
          )
            attempt.finish(error);
        },
      );
    }
    // Let synchronous React commit/cleanup work enroll or withdraw members
    // before deciding that this attempt's active set is complete.
    queueMicrotask(() => {
      if (this.recovery !== attempt) return;
      if ([...attempt.members.values()].every((member) => member.done))
        attempt.finish();
    });
  }

  private getOrCreateEntry<T>(
    key: string,
    options: UseInvalidatedQueryOptions,
    connectionEpoch: number,
  ): InvalidatedQueryEntry<T> {
    const normalized = normalizeOptions(options);
    let entry = this.entries.get(key) as InvalidatedQueryEntry<T> | undefined;
    if (!entry) {
      entry = new InvalidatedQueryEntry(key, normalized, connectionEpoch);
      this.entries.set(key, entry as InvalidatedQueryEntry<unknown>);
    } else if (optionsMismatch(entry.options, normalized)) {
      if (process.env.NODE_ENV === "development") {
        console.error(
          `[useInvalidatedQuery] semantic options mismatch for shared key "${key}"; keeping the existing entry options`,
        );
      }
    }
    return entry;
  }

  register<T>(
    key: string,
    fetcher: Fetcher<T>,
    options: UseInvalidatedQueryOptions,
    connectionEpoch: number,
  ): InvalidatedQueryRegistration<T> {
    let entry = this.getOrCreateEntry<T>(key, options, connectionEpoch);
    let registration = entry.register(fetcher);
    let disposed = false;
    return {
      key,
      getSnapshot: () => entry.getSnapshot(),
      subscribe: (listener) => entry.subscribe(listener),
      commit: (nextFetcher) => {
        entry.commit(registration, nextFetcher);
        this.syncRecovery();
      },
      revive: (nextFetcher, nextEpoch) => {
        if (!disposed) {
          entry.commit(registration, nextFetcher);
          this.syncRecovery();
          return;
        }
        entry = this.getOrCreateEntry<T>(key, options, nextEpoch);
        registration = entry.register(nextFetcher);
        entry.commit(registration, nextFetcher);
        disposed = false;
        this.syncRecovery();
      },
      setEnabled: (enabled) => {
        entry.setEnabled(registration, enabled);
        this.syncRecovery();
      },
      onEpoch: (epoch) => entry.onEpoch(epoch),
      invalidate: (mutation) => entry.invalidate(mutation),
      refetch: () => entry.refetch(),
      dispose: () => {
        if (disposed) return;
        disposed = true;
        if (entry.unregister(registration)) {
          entry.destroy();
          this.entries.delete(key);
        }
        this.syncRecovery();
      },
    };
  }
}

export const defaultInvalidatedQueryRegistry = new InvalidatedQueryRegistry();

export const InvalidatedQueryRegistryContext = createContext(
  defaultInvalidatedQueryRegistry,
);
