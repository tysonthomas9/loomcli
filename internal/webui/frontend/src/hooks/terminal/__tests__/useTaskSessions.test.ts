/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTaskSessions hook.
 * Covers initial state, fetching, adaptive polling, error handling, refetch, and cleanup.
 */

import { createElement, type ReactNode } from "react";
import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "@/hooks/common/queryRecovery";
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getTaskSessions } from "@/api/terminal";
import type { SessionRecord } from "@/types/agent";

import { useTaskSessions } from "../useTaskSessions";

const eventMock = vi.hoisted(() => ({
  connectionEpoch: 0,
  workspaceId: "test-ws-id",
}));

vi.mock("@/api/terminal", () => ({
  getTaskSessions: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: eventMock.workspaceId }),
  };
});

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return {
    ...actual,
    useEventSubscription: vi.fn(),
    useEventContext: () => ({ connectionEpoch: eventMock.connectionEpoch }),
  };
});

const mockGetSessions = vi.mocked(getTaskSessions);

function createMockSession(overrides?: Partial<SessionRecord>): SessionRecord {
  return {
    session_id: "session-1",
    task_id: "task-1",
    agent_name: "ember",
    backend: "claude",
    status: "completed",
    started_at: "2026-01-01T00:00:00Z",
    input_tokens: 100,
    output_tokens: 50,
    cache_read_tokens: 20,
    cache_write_tokens: 10,
    estimated_cost_usd: 0.01,
    exit_code: 0,
    files_changed: 2,
    lines_added: 50,
    lines_removed: 10,
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useTaskSessions", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    eventMock.connectionEpoch = 0;
    eventMock.workspaceId = "test-ws-id";
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns empty sessions when taskId is null", async () => {
      const { result } = renderHook(() => useTaskSessions(null));

      expect(result.current.sessions).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();

      await flushPromises();

      expect(mockGetSessions).not.toHaveBeenCalled();
    });
  });

  describe("fetching", () => {
    it("fetches sessions on mount with valid taskId", async () => {
      const sessions = [
        createMockSession({ session_id: "s1" }),
        createMockSession({ session_id: "s2" }),
      ];
      mockGetSessions.mockResolvedValueOnce(sessions);

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      expect(mockGetSessions).toHaveBeenCalledWith("test-ws-id", "task-1", {
        signal: expect.any(AbortSignal),
      });
      expect(result.current.sessions).toEqual(sessions);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("refetches its snapshot once per connection epoch", async () => {
      mockGetSessions.mockResolvedValue([createMockSession()]);
      const { rerender } = renderHook(() => useTaskSessions("task-1"));
      await flushPromises();
      expect(mockGetSessions).toHaveBeenCalledTimes(1);

      eventMock.connectionEpoch = 1;
      rerender();
      await flushPromises();
      expect(mockGetSessions).toHaveBeenCalledTimes(2);

      rerender();
      await flushPromises();
      expect(mockGetSessions).toHaveBeenCalledTimes(2);
    });

    it("resets sessions and refetches when taskId changes", async () => {
      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ session_id: "s1" }),
      ]);

      const { result, rerender } = renderHook(
        ({ taskId }: { taskId: string | null }) => useTaskSessions(taskId),
        { initialProps: { taskId: "task-1" } },
      );

      await flushPromises();
      expect(result.current.sessions).toHaveLength(1);

      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ session_id: "s2" }),
        createMockSession({ session_id: "s3" }),
      ]);

      rerender({ taskId: "task-2" });
      await flushPromises();

      expect(mockGetSessions).toHaveBeenLastCalledWith("test-ws-id", "task-2", {
        signal: expect.any(AbortSignal),
      });
    });

    it("clears sessions when taskId changes to null", async () => {
      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ session_id: "s1" }),
      ]);

      const { result, rerender } = renderHook(
        ({ taskId }: { taskId: string | null }) => useTaskSessions(taskId),
        { initialProps: { taskId: "task-1" as string | null } },
      );

      await flushPromises();
      expect(result.current.sessions).toHaveLength(1);

      rerender({ taskId: null });

      expect(result.current.sessions).toEqual([]);
      expect(result.current.error).toBeNull();
    });
  });

  describe("polling", () => {
    it("polls after initial fetch", async () => {
      mockGetSessions.mockResolvedValueOnce([createMockSession()]);

      renderHook(() => useTaskSessions("task-1"));

      await flushPromises();
      expect(mockGetSessions).toHaveBeenCalledTimes(1);

      mockGetSessions.mockResolvedValueOnce([createMockSession()]);

      // Poll at normal interval (10s)
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(mockGetSessions).toHaveBeenCalledTimes(2);
    });
  });

  describe("error handling", () => {
    it("sets error on fetch failure", async () => {
      mockGetSessions.mockRejectedValueOnce(new Error("Fetch failed"));

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Fetch failed");
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockGetSessions.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on successful subsequent fetch", async () => {
      mockGetSessions.mockRejectedValueOnce(new Error("Failed"));

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      mockGetSessions.mockResolvedValueOnce([createMockSession()]);

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
    });
  });

  describe("refetch", () => {
    it("manually triggers a refetch", async () => {
      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ session_id: "s1" }),
      ]);

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ session_id: "s1" }),
        createMockSession({ session_id: "s2" }),
      ]);

      await act(async () => {
        result.current.refetch();
      });
      await flushPromises();

      expect(mockGetSessions).toHaveBeenCalledTimes(2);
    });
  });

  describe("cleanup", () => {
    it("stops polling on unmount", async () => {
      mockGetSessions.mockResolvedValueOnce([createMockSession()]);

      const { unmount } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      unmount();

      mockGetSessions.mockClear();

      await act(async () => {
        vi.advanceTimersByTime(30000);
      });

      expect(mockGetSessions).not.toHaveBeenCalled();
    });

    it("does not update state after unmount", async () => {
      let resolveFetch!: (value: SessionRecord[]) => void;
      mockGetSessions.mockImplementationOnce(
        () =>
          new Promise<SessionRecord[]>((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { result, unmount } = renderHook(() => useTaskSessions("task-1"));
      await flushPromises();
      unmount();

      await act(async () => {
        resolveFetch([createMockSession()]);
        await Promise.resolve();
      });

      expect(result.current.sessions).toEqual([]);
    });
  });
});

function pendingSessions() {
  let resolve!: (data: SessionRecord[]) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<SessionRecord[]>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe("task session recovery", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetSessions.mockReset();
    eventMock.workspaceId = "test-ws-id";
    eventMock.connectionEpoch = 0;
  });
  it("requires a post-fence response and rejects recovery API failure", async () => {
    const recovery = new QueryRecoveryCoordinator("WS");
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        QueryRecoveryContext.Provider,
        { value: recovery },
        children,
      );
    const old = pendingSessions(),
      fresh = pendingSessions();
    mockGetSessions
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(fresh.promise);
    const hook = renderHook(() => useTaskSessions("task-1"), { wrapper });
    await flushPromises();
    let attempt!: Promise<void>;
    act(() => {
      attempt = recovery.refresh();
    });
    const rejected = expect(attempt).rejects.toThrow("recovery failed");
    await flushPromises();
    expect(mockGetSessions).toHaveBeenCalledTimes(2);
    eventMock.connectionEpoch += 1;
    hook.rerender();
    act(() => hook.result.current.refetch());
    await flushPromises();
    expect(mockGetSessions).toHaveBeenCalledTimes(2);
    await act(async () => {
      old.resolve([createMockSession({ session_id: "stale" })]);
      await Promise.resolve();
    });
    expect(hook.result.current.sessions).toEqual([]);
    await act(async () => {
      fresh.reject(new Error("recovery failed"));
      await rejected;
    });
    expect(hook.result.current.error?.message).toBe("recovery failed");
    hook.unmount();
  });
  it("connection epochs supersede pre-reconnect requests", async () => {
    const old = pendingSessions(),
      fresh = pendingSessions();
    mockGetSessions
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(fresh.promise);
    const hook = renderHook(() => useTaskSessions("task-1"));
    await flushPromises();
    eventMock.connectionEpoch = 1;
    hook.rerender();
    await flushPromises();
    expect(mockGetSessions).toHaveBeenCalledTimes(2);
    await act(async () => {
      fresh.resolve([createMockSession({ session_id: "fresh" })]);
    });
    await act(async () => {
      old.resolve([createMockSession({ session_id: "old" })]);
    });
    expect(hook.result.current.sessions[0]?.session_id).toBe("fresh");
    hook.unmount();
  });
  it("acknowledges only after refreshed sessions are committed", async () => {
    const recovery = new QueryRecoveryCoordinator("WS");
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        QueryRecoveryContext.Provider,
        { value: recovery },
        children,
      );
    mockGetSessions.mockResolvedValueOnce([]);
    const fresh = pendingSessions();
    mockGetSessions.mockReturnValueOnce(fresh.promise);
    const hook = renderHook(() => useTaskSessions("task-1"), { wrapper });
    await flushPromises();
    await flushPromises();
    let attempt!: Promise<void>;
    act(() => {
      attempt = recovery.refresh();
    });
    await flushPromises();
    await act(async () => {
      fresh.resolve([createMockSession({ session_id: "recovered" })]);
      await attempt;
    });
    expect(hook.result.current.sessions[0]?.session_id).toBe("recovered");
    hook.unmount();
  });
  it.each(["task", "workspace"])(
    "ignores late A→B→A %s responses",
    async (kind) => {
      const first = pendingSessions(),
        second = pendingSessions(),
        third = pendingSessions();
      mockGetSessions
        .mockReturnValueOnce(first.promise)
        .mockReturnValueOnce(second.promise)
        .mockReturnValueOnce(third.promise);
      const hook = renderHook(({ task }) => useTaskSessions(task), {
        initialProps: { task: "a" },
      });
      await flushPromises();
      if (kind === "workspace") eventMock.workspaceId = "other";
      hook.rerender({ task: kind === "task" ? "b" : "a" });
      await flushPromises();
      eventMock.workspaceId = "test-ws-id";
      hook.rerender({ task: "a" });
      await flushPromises();
      await act(async () => {
        third.resolve([createMockSession({ session_id: "current" })]);
        await Promise.resolve();
      });
      await act(async () => {
        first.resolve([createMockSession({ session_id: "old-a" })]);
        second.resolve([createMockSession({ session_id: "old-b" })]);
        await Promise.resolve();
      });
      expect(hook.result.current.sessions[0]?.session_id).toBe("current");
      hook.unmount();
    },
  );
  it("disabled task withdraws recovery and cannot receive a late completion", async () => {
    const recovery = new QueryRecoveryCoordinator("WS");
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        QueryRecoveryContext.Provider,
        { value: recovery },
        children,
      );
    const old = pendingSessions(),
      fresh = pendingSessions();
    mockGetSessions
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(fresh.promise);
    const hook = renderHook(
      ({ task }: { task: string | null }) => useTaskSessions(task),
      { initialProps: { task: "a" as string | null }, wrapper },
    );
    await flushPromises();
    let attempt!: Promise<void>;
    act(() => {
      attempt = recovery.refresh();
    });
    await flushPromises();
    hook.rerender({ task: null });
    await act(async () => {
      await attempt;
      fresh.resolve([createMockSession()]);
      old.resolve([createMockSession()]);
      await Promise.resolve();
    });
    expect(hook.result.current.sessions).toEqual([]);
    expect(hook.result.current.isLoading).toBe(false);
    hook.unmount();
  });
});
