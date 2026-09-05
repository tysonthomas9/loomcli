/**
 * @vitest-environment jsdom
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";

import type { MutationPayload } from "@/types/workspace";

import { EventContext, type EventContextValue } from "../useEventProvider";
import {
  InvalidatedQueryRegistry,
  InvalidatedQueryRegistryContext,
} from "../invalidatedQueryRegistry";
import { useInvalidatedQuery } from "../useInvalidatedQuery";

let registry: InvalidatedQueryRegistry;
let epoch = 0;
let listeners = new Set<(mutation: MutationPayload) => void>();
const subscribe = vi.fn(
  (listener: (mutation: MutationPayload) => void): (() => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
);

function Wrapper({ children }: { children: React.ReactNode }): JSX.Element {
  const state: EventContextValue["state"] = "connected";
  const value: EventContextValue = {
    state,
    reconnectAttempts: 0,
    lastError: null,
    isConnected: true,
    connectionEpoch: epoch,
    subscribe,
    onResync: () => () => {},
    retryNow: vi.fn(),
    disconnect: vi.fn(),
  };
  return React.createElement(
    InvalidatedQueryRegistryContext.Provider,
    { value: registry },
    React.createElement(EventContext.Provider, { value }, children),
  );
}

function emit(mutation: Partial<MutationPayload>): void {
  const payload: MutationPayload = {
    type: "update",
    timestamp: "2026-01-01T00:00:00Z",
    ...mutation,
  };
  act(() => {
    for (const listener of listeners) listener(payload);
  });
}

async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useInvalidatedQuery", () => {
  beforeEach(() => {
    registry = new InvalidatedQueryRegistry();
    epoch = 0;
    listeners = new Set();
    subscribe.mockClear();
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("fetches on mount and shares data with an equal-key instance", async () => {
    const fetcher = vi.fn().mockResolvedValue("loaded");
    const first = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "shared" }),
      { wrapper: Wrapper },
    );
    const second = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "shared" }),
      { wrapper: Wrapper },
    );

    await waitFor(() => expect(first.result.current.data).toBe("loaded"));
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(second.result.current.data).toBe("loaded");
  });

  it("does not abort a shared request when one instance unmounts", async () => {
    const request = deferred<string>();
    let signal: AbortSignal | undefined;
    const fetcher = vi.fn((nextSignal: AbortSignal) => {
      signal = nextSignal;
      return request.promise;
    });
    const first = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "shared" }),
      { wrapper: Wrapper },
    );
    const second = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "shared" }),
      { wrapper: Wrapper },
    );
    first.unmount();
    expect(signal?.aborted).toBe(false);

    await act(async () => request.resolve("survived"));
    expect(second.result.current.data).toBe("survived");
  });

  it("aborts and settles a pending refetch when the last enabled instance disables", async () => {
    const request = deferred<string>();
    let signal: AbortSignal | undefined;
    const fetcher = vi.fn((nextSignal: AbortSignal) => {
      signal = nextSignal;
      return request.promise;
    });
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useInvalidatedQuery(fetcher, { key: "query", enabled }),
      { initialProps: { enabled: true }, wrapper: Wrapper },
    );
    const pending = result.current.refetch();
    rerender({ enabled: false });
    await pending;
    expect(signal?.aborted).toBe(true);
    expect(result.current.loading).toBe(false);
  });

  it("guards stale key responses and retains or resets data as requested", async () => {
    const oldRequest = deferred<string>();
    const newRequest = deferred<string>();
    const fetcher = vi
      .fn<(_: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(oldRequest.promise)
      .mockReturnValueOnce(newRequest.promise);
    const { result, rerender } = renderHook(
      ({ key, reset }: { key: string; reset: boolean }) =>
        useInvalidatedQuery(fetcher, {
          key,
          resetOnKeyChange: reset,
        }),
      { initialProps: { key: "old", reset: false }, wrapper: Wrapper },
    );
    await act(async () => oldRequest.resolve("old-data"));
    expect(result.current.data).toBe("old-data");
    rerender({ key: "new", reset: false });
    expect(result.current.data).toBe("old-data");
    await act(async () => oldRequest.resolve("stale"));
    await act(async () => newRequest.resolve("new-data"));
    expect(result.current.data).toBe("new-data");

    const resetRequest = deferred<string>();
    const resetFetcher = vi.fn().mockReturnValue(resetRequest.promise);
    const resetHook = renderHook(
      ({ key }: { key: string }) =>
        useInvalidatedQuery(resetFetcher, { key, resetOnKeyChange: true }),
      { initialProps: { key: "one" }, wrapper: Wrapper },
    );
    resetHook.rerender({ key: "two" });
    expect(resetHook.result.current.data).toBeNull();
  });

  it("coalesces an event burst and runs one trailing fetch", async () => {
    vi.useFakeTimers();
    const first = deferred<string>();
    const second = deferred<string>();
    const fetcher = vi
      .fn<(_: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    renderHook(
      () =>
        useInvalidatedQuery(fetcher, {
          key: "query",
          entityTypes: ["issue"],
          debounceMs: 200,
        }),
      { wrapper: Wrapper },
    );
    await settle();
    emit({ entity_type: "issue", action: "issue.update" });
    emit({ entity_type: "issue", action: "issue.close" });
    vi.advanceTimersByTime(200);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => first.resolve("first"));
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => second.resolve("second"));
  });

  it("matches modern entity events before refresh/type fallbacks", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () =>
        useInvalidatedQuery(fetcher, {
          key: "query",
          entityTypes: ["issue", "dependency"],
          types: ["update"],
        }),
      { wrapper: Wrapper },
    );
    await settle();
    fetcher.mockClear();

    emit({ type: "refresh", entity_type: "agent", action: "agent.refresh" });
    emit({ type: "update", entity_type: "issue", action: "issue.update" });
    emit({ type: "refresh", entity_type: "" });
    emit({ type: "update", entity_type: "dep", action: "dep.add" });
    vi.advanceTimersByTime(200);
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("lets a type-only caller match entity-typed events by coarse type", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () =>
        useInvalidatedQuery(fetcher, {
          key: "legacy",
          types: ["update"],
        }),
      { wrapper: Wrapper },
    );
    await settle();
    fetcher.mockClear();

    emit({ type: "refresh", entity_type: "agent", action: "agent.refresh" });
    emit({ type: "status", entity_type: "issue", action: "issue.close" });
    vi.advanceTimersByTime(200);
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(0);

    emit({ type: "update", entity_type: "issue", action: "issue.update" });
    emit({ type: "update", entity_type: "dependency", action: "dep.add" });
    vi.advanceTimersByTime(200);
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("uses no safety poll by default and polls only when configured", async () => {
    vi.useFakeTimers();
    const offFetcher = vi.fn().mockResolvedValue("ok");
    renderHook(() => useInvalidatedQuery(offFetcher, { key: "off" }), {
      wrapper: Wrapper,
    });
    vi.advanceTimersByTime(1000);
    await settle();
    expect(offFetcher).toHaveBeenCalledTimes(1);

    const pollFetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () =>
        useInvalidatedQuery(pollFetcher, {
          key: "poll",
          safetyPollMs: 1000,
        }),
      { wrapper: Wrapper },
    );
    await settle();
    vi.advanceTimersByTime(1000);
    await settle();
    expect(pollFetcher).toHaveBeenCalledTimes(2);
  });

  it("skips hidden polls and fetches once on visibility", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () =>
        useInvalidatedQuery(fetcher, {
          key: "poll",
          safetyPollMs: 1000,
        }),
      { wrapper: Wrapper },
    );
    await settle();
    fetcher.mockClear();
    Object.defineProperty(document, "visibilityState", { value: "hidden" });
    vi.advanceTimersByTime(1000);
    await settle();
    expect(fetcher).not.toHaveBeenCalled();
    Object.defineProperty(document, "visibilityState", { value: "visible" });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("marks hidden events dirty and repairs once when visible", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () =>
        useInvalidatedQuery(fetcher, {
          key: "query",
          entityTypes: ["issue"],
        }),
      { wrapper: Wrapper },
    );
    await settle();
    fetcher.mockClear();
    Object.defineProperty(document, "visibilityState", { value: "hidden" });
    emit({ entity_type: "issue", action: "issue.update" });
    vi.advanceTimersByTime(200);
    expect(fetcher).not.toHaveBeenCalled();
    Object.defineProperty(document, "visibilityState", { value: "visible" });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("seeds the epoch at mount and invalidates after a later handshake", async () => {
    vi.useFakeTimers();
    epoch = 4;
    const fetcher = vi.fn().mockResolvedValue("ok");
    const { rerender } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query" }),
      { wrapper: Wrapper },
    );
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
    epoch = 5;
    rerender();
    vi.advanceTimersByTime(200);
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("logs and keeps the first sharer's semantic options", async () => {
    const originalNodeEnv = process.env.NODE_ENV;
    process.env.NODE_ENV = "development";
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const fetcher = vi.fn().mockResolvedValue("ok");
    renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query", debounceMs: 10 }),
      { wrapper: Wrapper },
    );
    renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query", debounceMs: 20 }),
      { wrapper: Wrapper },
    );
    expect(consoleSpy).toHaveBeenCalled();
    consoleSpy.mockRestore();
    process.env.NODE_ENV = originalNodeEnv;
  });

  it("keeps a stable snapshot identity across renders", async () => {
    const fetcher = vi.fn().mockResolvedValue("ok");
    const { result, rerender } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query" }),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.data).toBe("ok"));
    const first = result.current;
    rerender();
    expect(result.current.data).toBe(first.data);
    expect(result.current.loading).toBe(first.loading);
  });

  it("gates subscriptions and mount fetches on enabled", async () => {
    const fetcher = vi.fn().mockResolvedValue("ok");
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useInvalidatedQuery(fetcher, { key: "query", enabled }),
      { initialProps: { enabled: false }, wrapper: Wrapper },
    );
    expect(fetcher).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    rerender({ enabled: true });
    await waitFor(() => expect(result.current.data).toBe("ok"));
    expect(subscribe).toHaveBeenCalledTimes(1);
    rerender({ enabled: false });
    expect(listeners.size).toBe(0);
  });

  it("bypasses debounce and waits for a trailing refetch", async () => {
    vi.useFakeTimers();
    const first = deferred<string>();
    const second = deferred<string>();
    const fetcher = vi
      .fn<(_: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const { result } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query", debounceMs: 500 }),
      { wrapper: Wrapper },
    );
    const pending = result.current.refetch();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => first.resolve("first"));
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => second.resolve("second"));
    await pending;
    expect(result.current.data).toBe("second");
  });

  it("allows a disabled instance to refetch through the shared entry", async () => {
    const fetcher = vi.fn().mockResolvedValue("loaded");
    const { result } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "query", enabled: false }),
      { wrapper: Wrapper },
    );
    await act(async () => result.current.refetch());
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(result.current.data).toBe("loaded");
  });

  it("runs a trailing refetch requested by a disabled instance", async () => {
    const first = deferred<string>();
    const second = deferred<string>();
    const fetcher = vi
      .fn<(_: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const { result } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "disabled", enabled: false }),
      { wrapper: Wrapper },
    );

    const firstPending = result.current.refetch();
    const secondPending = result.current.refetch();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => first.resolve("first"));
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => second.resolve("second"));
    await firstPending;
    await secondPending;
    expect(result.current.data).toBe("second");
  });

  it("survives a StrictMode setup-cleanup-setup cycle", async () => {
    const first = deferred<string>();
    const second = deferred<string>();
    const fetcher = vi
      .fn<(_: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
      .mockResolvedValueOnce("manually refreshed");
    const { result } = renderHook(
      () => useInvalidatedQuery(fetcher, { key: "strict" }),
      {
        wrapper: ({ children }) =>
          React.createElement(
            React.StrictMode,
            null,
            React.createElement(Wrapper, null, children),
          ),
      },
    );
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher.mock.calls[0][0].aborted).toBe(true);
    await act(async () => second.resolve("fresh"));
    expect(result.current.data).toBe("fresh");
    await act(async () => first.resolve("stale aborted result"));
    expect(result.current.data).toBe("fresh");
    await act(async () => result.current.refetch());
    expect(result.current.data).toBe("manually refreshed");
  });
});
