/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useIssueDiffStat hook.
 * Covers initial fetch, disabled/null guards, polling lifecycle,
 * cleanup on unmount, and error handling.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchIssueDiffStat } from "@/api/diff-stat";
import type { IssueDiffStat } from "@/api/diff-stat";

import { useIssueDiffStat } from "../useIssueDiffStat";

vi.mock("@/api/diff-stat", () => ({
  fetchIssueDiffStat: vi.fn(),
}));

const mockFetch = vi.mocked(fetchIssueDiffStat);

function createMockDiffStat(overrides?: Partial<IssueDiffStat>): IssueDiffStat {
  return {
    branch: "feature-x",
    added: 10,
    removed: 3,
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useIssueDiffStat", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockFetch.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial fetch on mount", () => {
    it("fetches immediately when enabled with an issue ID", async () => {
      const diffStat = createMockDiffStat({ added: 42 });
      mockFetch.mockResolvedValueOnce(diffStat);

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();

      expect(mockFetch).toHaveBeenCalledWith("issue-123");
      expect(result.current.data).toEqual(diffStat);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("no fetch when disabled", () => {
    it("does not fetch when enabled is false", async () => {
      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: false }),
      );

      await flushPromises();

      expect(mockFetch).not.toHaveBeenCalled();
      expect(result.current.data).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("no fetch when issueId is null", () => {
    it("does not fetch when issueId is null", async () => {
      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: null, enabled: true }),
      );

      await flushPromises();

      expect(mockFetch).not.toHaveBeenCalled();
      expect(result.current.data).toBeNull();
    });
  });

  describe("polling", () => {
    it("polls at the configured interval (default 30s)", async () => {
      const diffStat1 = createMockDiffStat({ added: 1 });
      const diffStat2 = createMockDiffStat({ added: 2 });

      mockFetch.mockResolvedValueOnce(diffStat1);

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      // Initial fetch
      await flushPromises();
      expect(result.current.data?.added).toBe(1);
      expect(mockFetch).toHaveBeenCalledTimes(1);

      // Advance to trigger poll
      mockFetch.mockResolvedValueOnce(diffStat2);

      await act(async () => {
        vi.advanceTimersByTime(30000);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(result.current.data?.added).toBe(2);
    });

    it("does not poll before the interval elapses", async () => {
      mockFetch.mockResolvedValueOnce(createMockDiffStat());

      renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(29999);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(1);
    });

    it("respects custom pollInterval", async () => {
      mockFetch.mockResolvedValueOnce(createMockDiffStat());

      renderHook(() =>
        useIssueDiffStat({
          issueId: "issue-123",
          enabled: true,
          pollInterval: 5000,
        }),
      );

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      mockFetch.mockResolvedValueOnce(createMockDiffStat({ added: 99 }));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(2);
    });

    it("does not poll when pollInterval is 0", async () => {
      mockFetch.mockResolvedValueOnce(createMockDiffStat());

      renderHook(() =>
        useIssueDiffStat({
          issueId: "issue-123",
          enabled: true,
          pollInterval: 0,
        }),
      );

      await flushPromises();
      expect(mockFetch).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(60000);
      });
      await flushPromises();

      // Still only the initial fetch
      expect(mockFetch).toHaveBeenCalledTimes(1);
    });
  });

  describe("cleanup on unmount", () => {
    it("clears polling interval on unmount", async () => {
      const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");

      mockFetch.mockResolvedValueOnce(createMockDiffStat());

      const { unmount } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();

      clearIntervalSpy.mockClear();

      unmount();

      expect(clearIntervalSpy).toHaveBeenCalled();

      clearIntervalSpy.mockRestore();
    });

    it("does not update state after unmount", async () => {
      let resolvePromise!: (value: IssueDiffStat) => void;
      mockFetch.mockImplementationOnce(
        () =>
          new Promise<IssueDiffStat>((resolve) => {
            resolvePromise = resolve;
          }),
      );

      const { result, unmount } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      // Unmount before fetch resolves
      unmount();

      // Resolve after unmount -- should not throw or update state
      await act(async () => {
        resolvePromise(createMockDiffStat({ added: 99 }));
      });

      // Data should remain null (never updated after unmount)
      expect(result.current.data).toBeNull();
    });
  });

  describe("error handling", () => {
    it("sets error on fetch failure", async () => {
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Network error");
      expect(result.current.data).toBeNull();
    });

    it("wraps non-Error thrown values", async () => {
      mockFetch.mockRejectedValueOnce("string error");

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on successful subsequent fetch", async () => {
      // First fetch fails
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      // Poll interval triggers successful fetch
      mockFetch.mockResolvedValueOnce(createMockDiffStat());

      await act(async () => {
        vi.advanceTimersByTime(30000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.data).not.toBeNull();
    });
  });

  describe("refetch", () => {
    it("provides a refetch function that triggers a new fetch", async () => {
      mockFetch.mockResolvedValueOnce(createMockDiffStat({ added: 1 }));

      const { result } = renderHook(() =>
        useIssueDiffStat({ issueId: "issue-123", enabled: true }),
      );

      await flushPromises();
      expect(result.current.data?.added).toBe(1);

      mockFetch.mockResolvedValueOnce(createMockDiffStat({ added: 50 }));

      await act(async () => {
        await result.current.refetch();
      });

      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(result.current.data?.added).toBe(50);
    });
  });
});
