/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useBackends hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * mapping BackendHealthData through toBackendInfo(), and refetch.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchBackends } from "@/api/backends";
import type { BackendHealthData } from "@/api/backends";

import { useBackends } from "../useBackends";

// Mock the backends API module
vi.mock("@/api/backends", () => ({
  fetchBackends: vi.fn(),
}));

const mockFetchBackends = vi.mocked(fetchBackends);

/**
 * Helper to create a mock BackendHealthData.
 */
function createMockHealthData(
  overrides?: Partial<BackendHealthData>,
): BackendHealthData {
  return {
    name: "claude",
    display_name: "Claude",
    available: true,
    installed: true,
    api_key_set: true,
    ...overrides,
  };
}

/**
 * Helper to flush pending promises.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useBackends", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial loading state", () => {
    it("returns loading true and empty backends initially", async () => {
      mockFetchBackends.mockResolvedValueOnce([createMockHealthData()]);

      const { result } = renderHook(() => useBackends());

      expect(result.current.isLoading).toBe(true);
      expect(result.current.backends).toEqual([]);
      expect(result.current.error).toBeNull();

      await flushPromises();
    });

    it("sets loading false after fetch completes", async () => {
      mockFetchBackends.mockResolvedValueOnce([createMockHealthData()]);

      const { result } = renderHook(() => useBackends());

      expect(result.current.isLoading).toBe(true);

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("successful fetch with toBackendInfo mapping", () => {
    it("maps a known backend with defaults from backendDefaults", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "claude",
          display_name: "Claude",
          available: true,
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.backends).toHaveLength(1);
      const backend = result.current.backends[0];
      expect(backend.name).toBe("claude");
      expect(backend.displayName).toBe("Claude");
      expect(backend.available).toBe(true);
      // Known backend "claude" should get its brand defaults
      expect(backend.provider).toBe("Anthropic");
      expect(backend.brandColor).toBe("#d4a574");
    });

    it("maps an unknown backend with sensible fallback defaults", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "gemini",
          display_name: "Gemini Pro",
          available: false,
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.backends).toHaveLength(1);
      const backend = result.current.backends[0];
      expect(backend.name).toBe("gemini");
      // display_name from API should be used via apiData.displayName
      expect(backend.displayName).toBe("Gemini Pro");
      expect(backend.available).toBe(false);
      // Unknown backend falls back to defaults
      expect(backend.provider).toBe("Unknown");
      expect(backend.brandColor).toBe("#888888");
    });

    it("maps multiple backends", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "claude",
          display_name: "Claude",
          available: true,
        }),
        createMockHealthData({
          name: "codex",
          display_name: "Codex",
          available: false,
          message: "API key not set",
        }),
        createMockHealthData({
          name: "opencode",
          display_name: "OpenCode",
          available: true,
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.backends).toHaveLength(3);
      expect(result.current.backends[0].name).toBe("claude");
      expect(result.current.backends[1].name).toBe("codex");
      expect(result.current.backends[2].name).toBe("opencode");
    });

    it("passes healthMessage when API returns a message", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "codex",
          display_name: "Codex",
          available: false,
          message: "API key not configured",
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      const backend = result.current.backends[0];
      expect(backend.healthMessage).toBe("API key not configured");
    });

    it("does not set healthMessage when API message is absent", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "claude",
          display_name: "Claude",
          available: true,
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      const backend = result.current.backends[0];
      expect(backend.healthMessage).toBeUndefined();
    });

    it("returns empty backends array when API returns empty list", async () => {
      mockFetchBackends.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.backends).toEqual([]);
      expect(result.current.error).toBeNull();
    });
  });

  describe("error state", () => {
    it("sets error message on fetch failure with Error instance", async () => {
      mockFetchBackends.mockRejectedValueOnce(new Error("Server unavailable"));

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe("Server unavailable");
      expect(result.current.backends).toEqual([]);
    });

    it("sets generic error message for non-Error exceptions", async () => {
      mockFetchBackends.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe("Failed to fetch backends");
      expect(result.current.backends).toEqual([]);
    });
  });

  describe("refetch", () => {
    it("re-fetches backends from API when refetch is called", async () => {
      const initialData = [
        createMockHealthData({
          name: "claude",
          display_name: "Claude",
          available: true,
        }),
      ];
      const updatedData = [
        createMockHealthData({
          name: "claude",
          display_name: "Claude",
          available: true,
        }),
        createMockHealthData({
          name: "codex",
          display_name: "Codex",
          available: true,
        }),
      ];

      mockFetchBackends.mockResolvedValueOnce(initialData);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.backends).toHaveLength(1);
      expect(mockFetchBackends).toHaveBeenCalledTimes(1);

      // Setup new data for refetch
      mockFetchBackends.mockResolvedValueOnce(updatedData);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(mockFetchBackends).toHaveBeenCalledTimes(2);
      expect(result.current.backends).toHaveLength(2);
    });

    it("clears error on successful refetch", async () => {
      // Initial fetch fails
      mockFetchBackends.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.error).toBe("Network error");

      // Refetch succeeds
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({ name: "claude" }),
      ]);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.backends).toHaveLength(1);
    });

    it("sets loading true during refetch", async () => {
      mockFetchBackends.mockResolvedValueOnce([createMockHealthData()]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);

      // Setup a deferred promise for the refetch to control timing
      let resolveRefetch!: (value: BackendHealthData[]) => void;
      mockFetchBackends.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveRefetch = resolve;
          }),
      );

      act(() => {
        result.current.refetch();
      });

      // Need to flush to let the useEffect run
      await flushPromises();

      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveRefetch([createMockHealthData()]);
        await Promise.resolve();
      });

      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("unmount safety", () => {
    it("does not update state after unmount during fetch", async () => {
      let resolveFetch!: (value: BackendHealthData[]) => void;
      mockFetchBackends.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { unmount } = renderHook(() => useBackends());

      // Unmount before fetch resolves
      unmount();

      // Resolve the fetch - should not throw (cancelled flag prevents state update)
      await act(async () => {
        resolveFetch([createMockHealthData()]);
        await Promise.resolve();
      });

      // If we got here without errors, the test passes
    });

    it("does not update state after unmount during error", async () => {
      let rejectFetch!: (err: Error) => void;
      mockFetchBackends.mockImplementationOnce(
        () =>
          new Promise((_, reject) => {
            rejectFetch = reject;
          }),
      );

      const { unmount } = renderHook(() => useBackends());

      // Unmount before fetch rejects
      unmount();

      // Reject the fetch - should not throw
      await act(async () => {
        rejectFetch(new Error("Network error"));
        await Promise.resolve();
      });

      // If we got here without errors, the test passes
    });
  });
});
