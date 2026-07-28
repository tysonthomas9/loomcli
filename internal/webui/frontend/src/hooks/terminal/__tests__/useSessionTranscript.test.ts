/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useSessionTranscript hook.
 * Covers initial state, fetching, active polling, inactive single fetch, error handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  getAgentSessionTranscript,
  getSessionTranscript,
} from "@/api/terminal";
import type { TranscriptEntry } from "@/types/agent";
import { ApiError } from "@/types/common";

import { useSessionTranscript } from "../useSessionTranscript";

vi.mock("@/api/terminal", () => ({
  getAgentSessionTranscript: vi.fn(),
  getSessionTranscript: vi.fn(),
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

const mockGetTranscript = vi.mocked(getSessionTranscript);
const mockGetAgentTranscript = vi.mocked(getAgentSessionTranscript);

function createMockEntry(
  overrides?: Partial<TranscriptEntry>,
): TranscriptEntry {
  return {
    seq: 1,
    timestamp: "2026-01-01T00:00:00Z",
    role: "assistant",
    type: "text",
    text: "Hello world",
    ...overrides,
  };
}

function deferredTranscript(): {
  promise: Promise<TranscriptEntry[]>;
  resolve: (entries: TranscriptEntry[]) => void;
} {
  let resolve!: (entries: TranscriptEntry[]) => void;
  const promise = new Promise<TranscriptEntry[]>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useSessionTranscript", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockGetTranscript.mockReset();
    mockGetAgentTranscript.mockReset();
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
        createMockEntry({ seq: 1, role: "user", text: "Plan this" }),
        createMockEntry({ seq: 2, role: "assistant", text: "Sure" }),
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
        { preserveNotFound: true },
      );
      expect(result.current.entries).toEqual(entries);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("fetches a non-task interactive transcript by agent ownership", async () => {
      const entries = [
        createMockEntry({ seq: 1, role: "user", text: "Review this PR" }),
        createMockEntry({ seq: 2, text: "Review complete" }),
      ];
      mockGetAgentTranscript.mockResolvedValueOnce(entries);

      const { result } = renderHook(() =>
        useSessionTranscript(null, "sess-1", false, {
          agentId: "pr-reviewer",
        }),
      );

      await flushPromises();

      expect(mockGetAgentTranscript).toHaveBeenCalledWith(
        "test-ws-id",
        "pr-reviewer",
        "sess-1",
        { preserveNotFound: true },
      );
      expect(mockGetTranscript).not.toHaveBeenCalled();
      expect(result.current.entries).toEqual(entries);
    });

    it("keeps task ownership authoritative when both owner IDs are present", async () => {
      mockGetTranscript.mockResolvedValueOnce([createMockEntry()]);

      renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false, {
          agentId: "interactive-agent",
        }),
      );

      await flushPromises();

      expect(mockGetTranscript).toHaveBeenCalledWith(
        "test-ws-id",
        "task-1",
        "sess-1",
        { preserveNotFound: true },
      );
      expect(mockGetAgentTranscript).not.toHaveBeenCalled();
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
        { preserveNotFound: true },
      );

      mockGetTranscript.mockResolvedValueOnce([createMockEntry({ seq: 2 })]);

      rerender({ taskId: "task-2" });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenLastCalledWith(
        "test-ws-id",
        "task-2",
        "sess-1",
        { preserveNotFound: true },
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
        { preserveNotFound: true },
      );
    });

    it("resets the visible transcript while changed IDs are loading", async () => {
      const first = [createMockEntry({ text: "session one" })];
      mockGetTranscript.mockResolvedValueOnce(first);

      let resolveSecond!: (value: TranscriptEntry[]) => void;
      const { result, rerender } = renderHook(
        ({ sessionId }: { sessionId: string }) =>
          useSessionTranscript("task-1", sessionId, false),
        { initialProps: { sessionId: "sess-1" } },
      );

      await flushPromises();
      expect(result.current.entries).toEqual(first);

      mockGetTranscript.mockImplementationOnce(
        () =>
          new Promise<TranscriptEntry[]>((resolve) => {
            resolveSecond = resolve;
          }),
      );
      rerender({ sessionId: "sess-2" });

      expect(result.current.entries).toEqual([]);
      expect(result.current.isLoading).toBe(true);
      expect(result.current.error).toBeNull();

      const second = [createMockEntry({ text: "session two" })];
      await act(async () => {
        resolveSecond(second);
        await Promise.resolve();
      });
      expect(result.current.entries).toEqual(second);
    });

    it("ignores a stale response from the previous session", async () => {
      let resolveFirst!: (value: TranscriptEntry[]) => void;
      let resolveSecond!: (value: TranscriptEntry[]) => void;
      mockGetTranscript
        .mockImplementationOnce(
          () =>
            new Promise<TranscriptEntry[]>((resolve) => {
              resolveFirst = resolve;
            }),
        )
        .mockImplementationOnce(
          () =>
            new Promise<TranscriptEntry[]>((resolve) => {
              resolveSecond = resolve;
            }),
        );

      const { result, rerender } = renderHook(
        ({ sessionId }: { sessionId: string }) =>
          useSessionTranscript("task-1", sessionId, false),
        { initialProps: { sessionId: "sess-1" } },
      );
      rerender({ sessionId: "sess-2" });

      const second = [createMockEntry({ text: "current session" })];
      await act(async () => {
        resolveSecond(second);
        await Promise.resolve();
      });
      expect(result.current.entries).toEqual(second);

      await act(async () => {
        resolveFirst([createMockEntry({ text: "stale session" })]);
        await Promise.resolve();
      });
      expect(result.current.entries).toEqual(second);
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

    it("does not overlap active transcript reads when a request is slow", async () => {
      const first = deferredTranscript();
      mockGetTranscript.mockReturnValueOnce(first.promise);

      renderHook(() => useSessionTranscript("task-1", "sess-1", true));

      await flushPromises();
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(9_000);
      });
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      await act(async () => {
        first.resolve([createMockEntry()]);
        await Promise.resolve();
      });
      mockGetTranscript.mockResolvedValueOnce([createMockEntry({ seq: 2 })]);

      await act(async () => {
        vi.advanceTimersByTime(3_000);
        await Promise.resolve();
      });
      expect(mockGetTranscript).toHaveBeenCalledTimes(2);
    });

    it("keeps a missing active transcript pending between polls", async () => {
      const entries = [createMockEntry({ text: "projected transcript" })];
      mockGetTranscript
        .mockRejectedValueOnce(new ApiError(404, "Not Found"))
        .mockResolvedValueOnce(entries);

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", true),
      );

      await flushPromises();
      expect(result.current.entries).toEqual([]);
      expect(result.current.isLoading).toBe(true);
      expect(result.current.isUnavailable).toBe(false);
      expect(result.current.error).toBeNull();

      await act(async () => {
        vi.advanceTimersByTime(3_000);
        await Promise.resolve();
      });

      expect(result.current.entries).toEqual(entries);
      expect(result.current.isLoading).toBe(false);
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

    it("retries an unavailable inactive transcript until it succeeds", async () => {
      const entries = [createMockEntry({ text: "eventually available" })];
      mockGetTranscript
        .mockRejectedValueOnce(new ApiError(404, "Not Found"))
        .mockResolvedValueOnce(entries);

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false, {
          retryUnavailable: true,
        }),
      );

      await flushPromises();
      expect(result.current.entries).toEqual([]);
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(true);
      expect(result.current.isUnavailable).toBe(false);
      expect(mockGetTranscript).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(3000);
      });
      await flushPromises();

      expect(mockGetTranscript).toHaveBeenCalledTimes(2);
      expect(result.current.entries).toEqual(entries);
      expect(result.current.error).toBeNull();

      await act(async () => {
        vi.advanceTimersByTime(6000);
      });
      expect(mockGetTranscript).toHaveBeenCalledTimes(2);
    });

    it("clears the previous error as a bounded retry begins", async () => {
      const retry = deferredTranscript();
      mockGetTranscript
        .mockRejectedValueOnce(new Error("temporary transcript read failure"))
        .mockReturnValueOnce(retry.promise);

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false, {
          retryUnavailable: true,
        }),
      );

      await flushPromises();
      expect(result.current.error?.message).toBe(
        "temporary transcript read failure",
      );
      expect(result.current.isLoading).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(3_000);
        await Promise.resolve();
      });

      expect(mockGetTranscript).toHaveBeenCalledTimes(2);
      expect(result.current.isLoading).toBe(true);
      expect(result.current.error).toBeNull();

      const entries = [createMockEntry({ text: "retry succeeded" })];
      await act(async () => {
        retry.resolve(entries);
        await Promise.resolve();
      });
      expect(result.current.entries).toEqual(entries);
      expect(result.current.isLoading).toBe(false);
    });

    it("stops retrying and reports an unavailable inactive transcript", async () => {
      mockGetTranscript.mockRejectedValue(
        new ApiError(404, "Transcript not found"),
      );

      const { result } = renderHook(() =>
        useSessionTranscript("task-1", "sess-1", false, {
          retryUnavailable: true,
        }),
      );

      await flushPromises();
      expect(result.current.isUnavailable).toBe(false);

      for (let attempt = 0; attempt < 5; attempt += 1) {
        await act(async () => {
          vi.advanceTimersByTime(3_000);
          await Promise.resolve();
        });
      }

      expect(mockGetTranscript).toHaveBeenCalledTimes(6);
      expect(result.current.isUnavailable).toBe(true);
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(30_000);
      });
      expect(mockGetTranscript).toHaveBeenCalledTimes(6);
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

      await flushPromises();
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
