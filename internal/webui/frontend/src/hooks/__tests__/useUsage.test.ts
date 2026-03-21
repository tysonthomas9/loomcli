/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useUsage hook.
 * Covers initial state, fetching, polling, error handling, refetch, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchUsage } from "@/api";
import type { UsageResponse } from "@/types";

import { useUsage } from "../useUsage";

vi.mock("@/api", () => ({
  fetchUsage: vi.fn(),
}));

const mockFetchUsage = vi.mocked(fetchUsage);

function createMockUsage(overrides?: Partial<UsageResponse>): UsageResponse {
  return {
    total_input_tokens: 1000,
    total_output_tokens: 500,
    total_cache_read_tokens: 200,
    total_cache_write_tokens: 100,
    total_cost: 0.05,
    session_count: 3,
    by_agent: [],
    by_backend: [],
    daily_costs: [],
    sessions: [],
    timestamp: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useUsage", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("starts with null data and no error", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      const { result } = renderHook(() => useUsage());

      expect(result.current.data).toBeNull();
      expect(result.current.error).toBeNull();
      expect(result.current.lastUpdated).toBeNull();

      await flushPromises();
    });
  });

  describe("fetching", () => {
    it("fetches data on mount when enabled", async () => {
      const usage = createMockUsage({ total_cost: 1.5 });
      mockFetchUsage.mockResolvedValueOnce(usage);

      const { result } = renderHook(() => useUsage());

      await flushPromises();

      expect(mockFetchUsage).toHaveBeenCalledWith(undefined);
      expect(result.current.data).toEqual(usage);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.isConnected).toBe(true);
      expect(result.current.error).toBeNull();
      expect(result.current.lastUpdated).toBeInstanceOf(Date);
    });

    it("does not fetch when disabled", async () => {
      const { result } = renderHook(() =>
        useUsage({ enabled: false }),
      );

      await flushPromises();

      expect(mockFetchUsage).not.toHaveBeenCalled();
      expect(result.current.data).toBeNull();
    });

    it("passes params to fetchUsage", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      renderHook(() =>
        useUsage({ params: { agent: "nova", since: "2026-01-01" } }),
      );

      await flushPromises();

      expect(mockFetchUsage).toHaveBeenCalledWith({
        agent: "nova",
        since: "2026-01-01",
      });
    });
  });

  describe("polling", () => {
    it("polls at configured interval", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      renderHook(() => useUsage({ pollInterval: 5000 }));

      await flushPromises();
      expect(mockFetchUsage).toHaveBeenCalledTimes(1);

      mockFetchUsage.mockResolvedValueOnce(
        createMockUsage({ total_cost: 2.0 }),
      );

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockFetchUsage).toHaveBeenCalledTimes(2);
    });

    it("does not poll when pollInterval is 0", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      renderHook(() => useUsage({ pollInterval: 0 }));

      await flushPromises();
      expect(mockFetchUsage).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(60000);
      });

      expect(mockFetchUsage).toHaveBeenCalledTimes(1);
    });

    it("uses default 30s poll interval", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      renderHook(() => useUsage());

      await flushPromises();
      expect(mockFetchUsage).toHaveBeenCalledTimes(1);

      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      await act(async () => {
        vi.advanceTimersByTime(29999);
      });
      expect(mockFetchUsage).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      await flushPromises();

      expect(mockFetchUsage).toHaveBeenCalledTimes(2);
    });
  });

  describe("error handling", () => {
    it("sets error and disconnected state on failure", async () => {
      mockFetchUsage.mockRejectedValueOnce(new Error("Server down"));

      const { result } = renderHook(() => useUsage());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Server down");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockFetchUsage.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useUsage());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on subsequent successful fetch", async () => {
      mockFetchUsage.mockRejectedValueOnce(new Error("Server down"));

      const { result } = renderHook(() =>
        useUsage({ pollInterval: 1000 }),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();
      expect(result.current.isConnected).toBe(false);

      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

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
      mockFetchUsage.mockResolvedValueOnce(createMockUsage({ total_cost: 1.0 }));

      const { result } = renderHook(() => useUsage({ pollInterval: 0 }));

      await flushPromises();
      expect(result.current.data?.total_cost).toBe(1.0);

      mockFetchUsage.mockResolvedValueOnce(createMockUsage({ total_cost: 2.0 }));

      await act(async () => {
        await result.current.refetch();
      });

      expect(result.current.data?.total_cost).toBe(2.0);
      expect(mockFetchUsage).toHaveBeenCalledTimes(2);
    });
  });

  describe("cleanup", () => {
    it("clears polling interval on unmount", async () => {
      mockFetchUsage.mockResolvedValueOnce(createMockUsage());

      const { unmount } = renderHook(() =>
        useUsage({ pollInterval: 1000 }),
      );

      await flushPromises();

      unmount();

      mockFetchUsage.mockClear();

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });

      expect(mockFetchUsage).not.toHaveBeenCalled();
    });

    it("does not update state after unmount", async () => {
      let resolveFetch!: (value: UsageResponse) => void;
      mockFetchUsage.mockImplementationOnce(
        () =>
          new Promise<UsageResponse>((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { result, unmount } = renderHook(() => useUsage());

      unmount();

      await act(async () => {
        resolveFetch(createMockUsage());
        await Promise.resolve();
      });

      expect(result.current.data).toBeNull();
    });
  });
});
