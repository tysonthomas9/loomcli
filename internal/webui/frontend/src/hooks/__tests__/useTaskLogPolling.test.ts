/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTaskLogPolling hook.
 * Covers disabled state, polling lifecycle, content changes, error handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getTaskLogContent } from "@/api";

import { useTaskLogPolling } from "../useTaskLogPolling";

vi.mock("@/api", () => ({
  getTaskLogContent: vi.fn(),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
}));

const mockGetLog = vi.mocked(getTaskLogContent);

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useTaskLogPolling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("disabled state", () => {
    it("returns disconnected state when not enabled", () => {
      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: false,
        }),
      );

      expect(result.current.state).toBe("disconnected");
      expect(result.current.chunks).toEqual([]);
      expect(result.current.error).toBeNull();
    });

    it("returns disconnected state when taskId is null", () => {
      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: null,
          phase: "planning",
          enabled: true,
        }),
      );

      expect(result.current.state).toBe("disconnected");
      expect(result.current.chunks).toEqual([]);
    });

    it("returns disconnected state when phase is null", () => {
      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: null,
          enabled: true,
        }),
      );

      expect(result.current.state).toBe("disconnected");
    });

    it("does not call API when disabled", async () => {
      renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: false,
        }),
      );

      await flushPromises();

      expect(mockGetLog).not.toHaveBeenCalled();
    });
  });

  describe("fetching", () => {
    it("fetches immediately when enabled", async () => {
      mockGetLog.mockResolvedValueOnce({
        lines: ["line 1", "line 2"],
        lineCount: 2,
      });

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "implementation",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(mockGetLog).toHaveBeenCalledWith(
        "test-ws-id",
        "task-1",
        "implementation",
        500,
      );
      expect(result.current.state).toBe("connected");
      expect(result.current.chunks).toHaveLength(1);
      expect(result.current.error).toBeNull();
    });

    it("passes custom lines parameter", async () => {
      mockGetLog.mockResolvedValueOnce({ lines: [], lineCount: 0 });

      renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          lines: 100,
        }),
      );

      await flushPromises();

      expect(mockGetLog).toHaveBeenCalledWith(
        "test-ws-id",
        "task-1",
        "planning",
        100,
      );
    });
  });

  describe("polling", () => {
    it("polls at configured interval", async () => {
      mockGetLog.mockResolvedValueOnce({
        lines: ["line 1"],
        lineCount: 1,
      });

      renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();
      expect(mockGetLog).toHaveBeenCalledTimes(1);

      mockGetLog.mockResolvedValueOnce({
        lines: ["line 1", "line 2"],
        lineCount: 2,
      });

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(mockGetLog).toHaveBeenCalledTimes(2);
    });

    it("increments resetVersion when content changes", async () => {
      mockGetLog.mockResolvedValueOnce({
        lines: ["line 1"],
        lineCount: 1,
      });

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();
      const version1 = result.current.resetVersion;

      mockGetLog.mockResolvedValueOnce({
        lines: ["line 1", "new line"],
        lineCount: 2,
      });

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(result.current.resetVersion).toBeGreaterThan(version1);
    });

    it("does not update chunks when content is unchanged", async () => {
      const lines = ["line 1", "line 2"];
      mockGetLog.mockResolvedValueOnce({ lines, lineCount: 2 });

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();
      const version1 = result.current.resetVersion;
      const chunks1 = result.current.chunks;

      mockGetLog.mockResolvedValueOnce({ lines, lineCount: 2 });

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      // Same content = no re-render of chunks
      expect(result.current.resetVersion).toBe(version1);
      expect(result.current.chunks).toBe(chunks1);
    });
  });

  describe("error handling", () => {
    it("sets error on fetch failure", async () => {
      mockGetLog.mockRejectedValueOnce(new Error("Fetch failed"));

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBe("Fetch failed");
      expect(result.current.state).toBe("disconnected");
    });

    it("sets reconnecting state on error after successful connection", async () => {
      mockGetLog.mockResolvedValueOnce({ lines: ["ok"], lineCount: 1 });

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();
      expect(result.current.state).toBe("connected");

      mockGetLog.mockRejectedValueOnce(new Error("Connection lost"));

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(result.current.state).toBe("reconnecting");
      expect(result.current.error).toBe("Connection lost");
    });

    it("uses fallback message for non-Error thrown values", async () => {
      mockGetLog.mockRejectedValueOnce("string error");

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBe("Failed to fetch task logs");
    });

    it("clears error on successful subsequent fetch", async () => {
      mockGetLog.mockRejectedValueOnce(new Error("Fetch failed"));

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      mockGetLog.mockResolvedValueOnce({ lines: ["recovered"], lineCount: 1 });

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.state).toBe("connected");
    });
  });

  describe("refresh", () => {
    it("triggers a new fetch cycle by incrementing reloadKey", async () => {
      mockGetLog.mockResolvedValueOnce({ lines: ["v1"], lineCount: 1 });

      const { result } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
        }),
      );

      await flushPromises();
      expect(mockGetLog).toHaveBeenCalledTimes(1);

      mockGetLog.mockResolvedValueOnce({ lines: ["v2"], lineCount: 1 });

      act(() => {
        result.current.refresh();
      });
      await flushPromises();

      expect(mockGetLog).toHaveBeenCalledTimes(2);
    });
  });

  describe("cleanup", () => {
    it("cancels polling on unmount", async () => {
      mockGetLog.mockResolvedValueOnce({ lines: ["ok"], lineCount: 1 });

      const { unmount } = renderHook(() =>
        useTaskLogPolling({
          taskId: "task-1",
          phase: "planning",
          enabled: true,
          pollIntervalMs: 1000,
        }),
      );

      await flushPromises();

      unmount();

      mockGetLog.mockClear();

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });

      expect(mockGetLog).not.toHaveBeenCalled();
    });

    it("resets state when disabled after being enabled", async () => {
      mockGetLog.mockResolvedValueOnce({ lines: ["ok"], lineCount: 1 });

      const { result, rerender } = renderHook(
        ({ enabled }: { enabled: boolean }) =>
          useTaskLogPolling({
            taskId: "task-1",
            phase: "planning",
            enabled,
          }),
        { initialProps: { enabled: true } },
      );

      await flushPromises();
      expect(result.current.state).toBe("connected");

      rerender({ enabled: false });

      expect(result.current.state).toBe("disconnected");
      expect(result.current.chunks).toEqual([]);
      expect(result.current.error).toBeNull();
    });
  });
});
