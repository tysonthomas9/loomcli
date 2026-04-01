/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTaskSessions hook.
 * Covers initial state, fetching, adaptive polling, error handling, refetch, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getTaskSessions } from "@/api/sessions";
import type { SessionRecord } from "@/types/session";

import { useTaskSessions } from "../useTaskSessions";

vi.mock("@/api/sessions", () => ({
  getTaskSessions: vi.fn(),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
}));

const mockGetSessions = vi.mocked(getTaskSessions);

function createMockSession(overrides?: Partial<SessionRecord>): SessionRecord {
  return {
    id: "session-1",
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
        createMockSession({ id: "s1" }),
        createMockSession({ id: "s2" }),
      ];
      mockGetSessions.mockResolvedValueOnce(sessions);

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      expect(mockGetSessions).toHaveBeenCalledWith("test-ws-id", "task-1");
      expect(result.current.sessions).toEqual(sessions);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("resets sessions and refetches when taskId changes", async () => {
      mockGetSessions.mockResolvedValueOnce([createMockSession({ id: "s1" })]);

      const { result, rerender } = renderHook(
        ({ taskId }: { taskId: string | null }) => useTaskSessions(taskId),
        { initialProps: { taskId: "task-1" } },
      );

      await flushPromises();
      expect(result.current.sessions).toHaveLength(1);

      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ id: "s2" }),
        createMockSession({ id: "s3" }),
      ]);

      rerender({ taskId: "task-2" });
      await flushPromises();

      expect(mockGetSessions).toHaveBeenLastCalledWith("test-ws-id", "task-2");
    });

    it("clears sessions when taskId changes to null", async () => {
      mockGetSessions.mockResolvedValueOnce([createMockSession({ id: "s1" })]);

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
      mockGetSessions.mockResolvedValueOnce([createMockSession({ id: "s1" })]);

      const { result } = renderHook(() => useTaskSessions("task-1"));

      await flushPromises();

      mockGetSessions.mockResolvedValueOnce([
        createMockSession({ id: "s1" }),
        createMockSession({ id: "s2" }),
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

      unmount();

      await act(async () => {
        resolveFetch([createMockSession()]);
        await Promise.resolve();
      });

      expect(result.current.sessions).toEqual([]);
    });
  });
});
