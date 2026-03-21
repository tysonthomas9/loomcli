/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useObservabilityMetrics hook.
 * Covers initial state, fetching, polling, visibility change, error handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchObservabilityMetrics } from "@/api";
import type { MetricsSnapshot } from "@/types";

import { useObservabilityMetrics } from "../useObservabilityMetrics";

vi.mock("@/api", () => ({
  fetchObservabilityMetrics: vi.fn(),
}));

const mockFetch = vi.mocked(fetchObservabilityMetrics);

function createMockMetrics(
  overrides?: Partial<MetricsSnapshot>,
): MetricsSnapshot {
  return {
    timestamp: "2026-01-01T00:00:00Z",
    tasks_completed_last_hour: 5,
    tasks_completed_24h: 42,
    avg_task_duration_sec: 120,
    lines_changed_last_hour: 500,
    error_rate_pct: 2.5,
    restart_count_24h: 1,
    restarts_by_agent: {},
    agent_utilization: {},
    tasks_by_role: {},
    tasks_by_epic: {},
    tasks_by_agent: {},
    hourly_completions: [],
    total_tasks_completed: 100,
    total_tasks_failed: 5,
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useObservabilityMetrics", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("starts with null metrics and not connected", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      const { result } = renderHook(() => useObservabilityMetrics());

      expect(result.current.metrics).toBeNull();
      expect(result.current.isConnected).toBe(false);
      expect(result.current.error).toBeNull();
      expect(result.current.lastUpdated).toBeNull();

      await flushPromises();
    });
  });

  describe("fetching", () => {
    it("fetches metrics on mount when enabled", async () => {
      const metrics = createMockMetrics({ tasks_completed_24h: 99 });
      mockFetch.mockResolvedValueOnce(metrics);

      const { result } = renderHook(() => useObservabilityMetrics());

      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(1);
      expect(result.current.metrics).toEqual(metrics);
      expect(result.current.isConnected).toBe(true);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
      expect(result.current.lastUpdated).toBeInstanceOf(Date);
    });

    it("does not fetch when disabled", async () => {
      const { result } = renderHook(() =>
        useObservabilityMetrics({ enabled: false }),
      );

      await flushPromises();

      expect(mockFetch).not.toHaveBeenCalled();
      expect(result.current.metrics).toBeNull();
    });
  });

  describe("polling", () => {
    it("polls at configured interval", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      renderHook(() => useObservabilityMetrics({ pollInterval: 5000 }));

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      mockFetch.mockResolvedValueOnce(
        createMockMetrics({ tasks_completed_24h: 50 }),
      );

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(2);
    });

    it("does not poll when pollInterval is 0", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      renderHook(() => useObservabilityMetrics({ pollInterval: 0 }));

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(60000);
      });

      expect(mockFetch).toHaveBeenCalledTimes(1);
    });

    it("uses default 30s poll interval", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      renderHook(() => useObservabilityMetrics());

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      mockFetch.mockResolvedValueOnce(createMockMetrics());

      await act(async () => {
        vi.advanceTimersByTime(29999);
      });
      expect(mockFetch).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(2);
    });
  });

  describe("visibility change", () => {
    it("refetches when page becomes visible", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      renderHook(() => useObservabilityMetrics());

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      mockFetch.mockResolvedValueOnce(createMockMetrics());

      // Simulate tab becoming visible
      Object.defineProperty(document, "visibilityState", {
        value: "visible",
        writable: true,
        configurable: true,
      });

      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(2);
    });

    it("does not refetch when page becomes hidden", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      renderHook(() => useObservabilityMetrics());

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        writable: true,
        configurable: true,
      });

      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(1);
    });
  });

  describe("error handling", () => {
    it("sets error and disconnected state on failure", async () => {
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useObservabilityMetrics());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Network error");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockFetch.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useObservabilityMetrics());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on successful subsequent fetch", async () => {
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() =>
        useObservabilityMetrics({ pollInterval: 1000 }),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      mockFetch.mockResolvedValueOnce(createMockMetrics());

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.isConnected).toBe(true);
    });
  });

  describe("refetch", () => {
    it("manually triggers a refetch", async () => {
      mockFetch.mockResolvedValueOnce(createMockMetrics({ error_rate_pct: 5 }));

      const { result } = renderHook(() =>
        useObservabilityMetrics({ pollInterval: 0 }),
      );

      await flushPromises();
      expect(result.current.metrics?.error_rate_pct).toBe(5);

      mockFetch.mockResolvedValueOnce(createMockMetrics({ error_rate_pct: 1 }));

      await act(async () => {
        await result.current.refetch();
      });

      expect(result.current.metrics?.error_rate_pct).toBe(1);
      expect(mockFetch).toHaveBeenCalledTimes(2);
    });
  });

  describe("cleanup", () => {
    it("clears polling interval and event listener on unmount", async () => {
      const removeEventSpy = vi.spyOn(document, "removeEventListener");
      mockFetch.mockResolvedValueOnce(createMockMetrics());

      const { unmount } = renderHook(() =>
        useObservabilityMetrics({ pollInterval: 1000 }),
      );

      await flushPromises();

      unmount();

      expect(removeEventSpy).toHaveBeenCalledWith(
        "visibilitychange",
        expect.any(Function),
      );

      mockFetch.mockClear();

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });

      expect(mockFetch).not.toHaveBeenCalled();

      removeEventSpy.mockRestore();
    });

    it("does not update state after unmount", async () => {
      let resolveFetch!: (value: MetricsSnapshot) => void;
      mockFetch.mockImplementationOnce(
        () =>
          new Promise<MetricsSnapshot>((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { result, unmount } = renderHook(() => useObservabilityMetrics());

      unmount();

      await act(async () => {
        resolveFetch(createMockMetrics());
        await Promise.resolve();
      });

      expect(result.current.metrics).toBeNull();
    });
  });
});
