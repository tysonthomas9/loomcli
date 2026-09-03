/**
 * @vitest-environment jsdom
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";

import { getBlockedIssues } from "@/api/issues";
import type { MutationPayload } from "@/types/workspace";
import type { BlockedIssue } from "@/types";
import {
  EventContext,
  InvalidatedQueryRegistry,
  InvalidatedQueryRegistryContext,
  type EventContextValue,
} from "@/hooks/common";

import { useBlockedIssues } from "../useBlockedIssues";

vi.mock("@/api/issues", () => ({
  getBlockedIssues: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

const mockGetBlockedIssues = vi.mocked(getBlockedIssues);

function createBlockedIssue(id = "issue-1"): BlockedIssue {
  return {
    id,
    title: `Blocked ${id}`,
    priority: 2,
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    blocked_by_count: 1,
    blocked_by: ["blocker-1"],
  };
}

let epoch = 0;
let state: EventContextValue["state"] = "connected";
let eventListeners = new Set<(mutation: MutationPayload) => void>();
let registry = new InvalidatedQueryRegistry();

const subscribe = vi.fn(
  (callback: (mutation: MutationPayload) => void): (() => void) => {
    eventListeners.add(callback);
    return () => eventListeners.delete(callback);
  },
);

function Wrapper({ children }: { children: React.ReactNode }): JSX.Element {
  const context: EventContextValue = {
    state,
    reconnectAttempts: 0,
    lastError: null,
    isConnected: state === "connected",
    connectionEpoch: epoch,
    subscribe,
    onResync: () => () => {},
    retryNow: vi.fn(),
    disconnect: vi.fn(),
  };
  return React.createElement(
    InvalidatedQueryRegistryContext.Provider,
    { value: registry },
    React.createElement(EventContext.Provider, { value: context }, children),
  );
}

function emit(mutation: Partial<MutationPayload>): void {
  const payload: MutationPayload = {
    type: "update",
    timestamp: "2025-01-01T00:00:00Z",
    ...mutation,
  };
  act(() => {
    for (const listener of eventListeners) listener(payload);
  });
}

async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useBlockedIssues", () => {
  beforeEach(() => {
    mockGetBlockedIssues.mockReset();
    eventListeners = new Set();
    registry = new InvalidatedQueryRegistry();
    epoch = 0;
    state = "connected";
    subscribe.mockClear();
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("fetches on mount and forwards the request signal", async () => {
    mockGetBlockedIssues.mockResolvedValue([]);
    const { result } = renderHook(() => useBlockedIssues(), {
      wrapper: Wrapper,
    });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.data).toEqual([]);
    expect(mockGetBlockedIssues).toHaveBeenCalledWith(
      "test-ws-id",
      {},
      { signal: expect.any(AbortSignal) },
    );
  });

  it("keeps data on errors and clears the error after success", async () => {
    const data = [createBlockedIssue()];
    mockGetBlockedIssues.mockResolvedValueOnce(data);
    const { result } = renderHook(() => useBlockedIssues(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.data).toEqual(data));

    mockGetBlockedIssues.mockRejectedValueOnce(new Error("network"));
    await act(async () => result.current.refetch());
    expect(result.current.data).toEqual(data);
    expect(result.current.error).toEqual(new Error("network"));

    mockGetBlockedIssues.mockResolvedValueOnce([]);
    await act(async () => result.current.refetch());
    expect(result.current.error).toBeNull();
    expect(result.current.data).toEqual([]);
  });

  it("passes filters and refetches when the key changes", async () => {
    mockGetBlockedIssues.mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ parentId }: { parentId?: string }) => useBlockedIssues({ parentId }),
      { initialProps: { parentId: "epic-1" }, wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mockGetBlockedIssues).toHaveBeenCalledWith(
      "test-ws-id",
      { parent_id: "epic-1" },
      { signal: expect.any(AbortSignal) },
    );

    rerender({ parentId: "epic-2" });
    await waitFor(() =>
      expect(mockGetBlockedIssues).toHaveBeenLastCalledWith(
        "test-ws-id",
        { parent_id: "epic-2" },
        { signal: expect.any(AbortSignal) },
      ),
    );
  });

  it("shares one mount request and one debounced event fetch", async () => {
    mockGetBlockedIssues.mockResolvedValue([]);
    const first = renderHook(() => useBlockedIssues(), { wrapper: Wrapper });
    const second = renderHook(() => useBlockedIssues(), { wrapper: Wrapper });
    await waitFor(() => expect(first.result.current.loading).toBe(false));
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

    emit({ entity_type: "issue", action: "issue.update" });
    emit({ entity_type: "dependency", action: "dep.add" });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 250));
    });
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2);
    expect(second.result.current.data).toEqual([]);
  });

  it("uses entity_type precedence and ignores legacy dep", async () => {
    vi.useFakeTimers();
    mockGetBlockedIssues.mockResolvedValue([]);
    renderHook(() => useBlockedIssues(), { wrapper: Wrapper });
    await settle();
    mockGetBlockedIssues.mockClear();

    emit({ type: "refresh", entity_type: "agent", action: "agent.refresh" });
    emit({ type: "refresh", entity_type: "" });
    emit({ type: "update", entity_type: "dep", action: "dep.add" });
    vi.advanceTimersByTime(200);
    await settle();
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
  });

  it("does not fetch while disabled, but disabled refetch still uses the entry", async () => {
    mockGetBlockedIssues.mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useBlockedIssues({ enabled }),
      { initialProps: { enabled: false }, wrapper: Wrapper },
    );
    expect(mockGetBlockedIssues).not.toHaveBeenCalled();

    await act(async () => result.current.refetch());
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
    rerender({ enabled: true });
    await waitFor(() => expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2));
  });

  it("refetches once on a completed connection epoch", async () => {
    vi.useFakeTimers();
    mockGetBlockedIssues.mockResolvedValue([]);
    const { rerender } = renderHook(() => useBlockedIssues(), {
      wrapper: Wrapper,
    });
    await settle();
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

    epoch = 1;
    rerender();
    vi.advanceTimersByTime(200);
    await settle();
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2);
  });

  it("does one immediate repair fetch when the document becomes visible", async () => {
    vi.useFakeTimers();
    mockGetBlockedIssues.mockResolvedValue([]);
    renderHook(() => useBlockedIssues(), { wrapper: Wrapper });
    await settle();
    mockGetBlockedIssues.mockClear();

    Object.defineProperty(document, "visibilityState", { value: "hidden" });
    emit({ entity_type: "issue", action: "issue.update" });
    vi.advanceTimersByTime(250);
    await settle();
    expect(mockGetBlockedIssues).not.toHaveBeenCalled();

    Object.defineProperty(document, "visibilityState", { value: "visible" });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await settle();
    expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
  });

  it("silently ignores AbortError and aborts an in-flight request on unmount", async () => {
    let rejectRequest: (error: unknown) => void = () => {};
    mockGetBlockedIssues.mockReturnValue(
      new Promise<BlockedIssue[]>((_, reject) => {
        rejectRequest = reject;
      }),
    );
    const { result, unmount } = renderHook(() => useBlockedIssues(), {
      wrapper: Wrapper,
    });
    const refetchPromise = act(async () => result.current.refetch());
    unmount();
    rejectRequest(new DOMException("aborted", "AbortError"));
    await refetchPromise;
    expect(result.current.error).toBeNull();
  });
});
