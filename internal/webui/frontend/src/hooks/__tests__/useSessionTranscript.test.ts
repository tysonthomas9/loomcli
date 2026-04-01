/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useSessionTranscript hook.
 * Covers initial state, fetching, active polling, inactive single fetch, error handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getSessionTranscript } from "@/api/sessions";
import type { TranscriptEntry } from "@/types/session";

import { useSessionTranscript } from "../useSessionTranscript";

vi.mock("@/api/sessions", () => ({
  getSessionTranscript: vi.fn(),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
}));

const mockGetTranscript = vi.mocked(getSessionTranscript);

function createMockEntry(
  overrides?: Partial<TranscriptEntry>,
): TranscriptEntry {
  return {
    seq: 1,
    ts: "2026-01-01T00:00:00Z",
    role: "assistant",
    type: "text",
    content: "Hello world",
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useSessionTranscript", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns empty entries when taskId is null", async () => {
      const { result } = renderHook(() =>
        useSessionTranscript(null, "sess-1", false),
      );

      expect(result.current.entries).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();

      await flushPromises();

      expect(mockGetTranscript).not.toHaveBeenCalled();
    });

    it("returns empty entries when sessionId is null", async () => {
      const { result } = renderHook(() =>
        useSessionTranscript("task-1", null, false),
      );

      expect(result.current.entries).toEqual([]);
      expect(mockGetTranscript).not.toHaveBeenCalled();
    });
  });

  describe("fetching", () => {
    it("fetches transcript on mount with valid IDs", async () => {
      const entries = [
        createMockEntry({ seq: 1, role: "user", content: "Plan this" }),
        createMockEntry({ seq: 2, role: "assistant", content: "Sure" }),
      ];
      mockGetTranscript.mockResolvedValueOnce(entries);

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false),
      );

      await flushPromises();

      expect(mockGetTranscript).toHaveBeenCalledWith(
        "test-ws-id",
        "task-1",
        "sess-1",
      );
      expect(result.current.entries).toEqual(entries);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("refetches when taskId changes", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      const { rerender } = renderHook(
        ({ taskId }: { taskId: string }) =>
          useSessionTranscript(taskId, "sess-1", false),
        { initialProps: { taskId: "task-1" } },
      );

      await flushPromises();
      expect(mockGetTranscript).toHaveBeenCalledWith(
        "test-ws-id",
        "task-1",
        "sess-1",
      );

      mockGetTranscript.mockResolvedValueOnce([createMockEntry({ seq: 2 })]);

      rerender({ taskId: "task-2" });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenLastCalledWith(
        "test-ws-id",
        "task-2",
        "sess-1",
      );
    });

    it("refetches when sessionId changes", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      const { rerender } = renderHook(
        ({ sessionId }: { sessionId: string }) =>
          useSessionTranscript("task-1", sessionId, false),
        { initialProps: { sessionId: "sess-1" } },
      );

      await flushPromises();

      mockGetTranscript.mockResolvedValueOnce([createMockEntry({ seq: 2 })]);

      rerender({ sessionId: "sess-2" });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenLastCalledWith(
        "test-ws-id",
        "task-1",
        "sess-2",
      );
    });
  });

  describe("polling when active", () => {
    it("polls every 3s when isActive is true", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      renderHook(() => useSessionTranscript("task-1", "sess-1", true));

      await flushPromises();
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      mockGetTranscript.mockResolvedValueOnce([
        createMockEntry(),
        createMockEntry({ seq: 2 }),
      ]);

      await act(async () => {
        vi.advanceTimersByTime(3000);
      });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenCalledTimes(2);
    });

    it("does not poll when isActive is false", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      renderHook(() => useSessionTranscript("task-1", "sess-1", false));

      await flushPromises();
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });

      // No additional calls
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);
    });

    it("starts polling when isActive transitions to true", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      const { rerender } = renderHook(
        ({ active }: { active: boolean }) =>
          useSessionTranscript("task-1", "sess-1", active),
        { initialProps: { active: false } },
      );

      await flushPromises();
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      rerender({ active: true });
      await flushPromises();

      // Refetch on rerender
      expect(mockGetTranscript).toHaveBeenCalledTimes(2);

      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      await act(async () => {
        vi.advanceTimersByTime(3000);
      });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenCalledTimes(3);
    });
  });

  describe("error handling", () => {
    it("sets error on fetch failure", async () => {
      mockGetTranscript.mockRejectedValueOnce(new Error("Fetch failed"));

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Fetch failed");
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockGetTranscript.mockRejectedValueOnce("string error");

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on successful subsequent fetch", async () => {
      mockGetTranscript.mockRejectedValueOnce(new Error("Failed"));

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", true),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      await act(async () => {
        vi.advanceTimersByTime(3000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
    });
  });

  describe("cleanup", () => {
    it("stops polling on unmount", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      const { unmount } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", true),
      );

      await flushPromises();

      unmount();

      mockGetTranscript.mockClear();

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });

      expect(mockGetTranscript).not.toHaveBeenCalled();
    });

    it("does not update state after unmount", async () => {
      let resolveFetch!: (value: TranscriptEntry[]) => void;
      mockGetTranscript.mockImplementationOnce(
        () =>
          new Promise<TranscriptEntry[]>((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { result, unmount } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false),
      );

      unmount();

      await act(async () => {
        resolveFetch([createMockEntry()]);
        await Promise.resolve();
      });

      expect(result.current.entries).toEqual([]);
    });

    it("clears entries when IDs become null", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      const { result, rerender } = renderHook(
        ({
          taskId,
          sessionId,
        }: {
          taskId: string | null;
          sessionId: string | null;
        }) => useSessionTranscript(taskId, sessionId, false),
        {
          initialProps: {
            taskId: "task-1" as string | null,
            sessionId: "sess-1" as string | null,
          },
        },
      );

      await flushPromises();
      expect(result.current.entries).toHaveLength(1);

      rerender({ taskId: null, sessionId: null });

      expect(result.current.entries).toEqual([]);
      expect(result.current.error).toBeNull();
    });
  });
});
