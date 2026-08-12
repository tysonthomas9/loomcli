/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useBackends hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * mapping BackendHealthData through toBackendInfo(), and refetch.
 * The hook delegates to backendsStore (Zustand vanilla store).
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { BackendHealthData } from "@/api/workspace";

// Mock the backends API module (used by the store internally)
vi.mock("@/api/workspace", () => ({
  fetchBackends: vi.fn(),
  refreshBackends: vi.fn(),
}));

// Mock @/stores so we can provide a fresh backendsStore per test
vi.mock("@/stores", async (importOriginal) => {
  const original = (await importOriginal()) as typeof import("@/stores");
  return {
    ...original,
    backendsStore: original.createBackendsStore(),
  };
});

import { backendsStore } from "@/stores";
import { fetchBackends } from "@/api/workspace";

const mockFetchBackends = vi.mocked(fetchBackends);

import { useBackends } from "../useBackends";

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
    backendsStore.getState().reset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial loading state", () => {
    it("returns empty backends initially", async () => {
      mockFetchBackends.mockResolvedValueOnce([createMockHealthData()]);

      const { result } = renderHook(() => useBackends());

      expect(result.current.backends).toEqual([]);
      expect(result.current.error).toBeNull();

      await flushPromises();
    });

    it("does not fetch until an explicitly disabled consumer becomes active", async () => {
      mockFetchBackends.mockResolvedValueOnce([createMockHealthData()]);

      const { rerender } = renderHook(
        ({ enabled }: { enabled: boolean }) => useBackends(enabled),
        { initialProps: { enabled: false } },
      );
      await flushPromises();
      expect(mockFetchBackends).not.toHaveBeenCalled();

      rerender({ enabled: true });
      await flushPromises();
      expect(mockFetchBackends).toHaveBeenCalledTimes(1);
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
      expect(backend.installed).toBe(true);
      expect(backend.apiKeySet).toBe(true);
      // Known backend "claude" should get its brand defaults
      expect(backend.provider).toBe("Anthropic");
      expect(backend.brandColor).toBe("#d4a574");
    });

    it("maps an unknown backend with sensible fallback defaults", async () => {
      mockFetchBackends.mockResolvedValueOnce([
        createMockHealthData({
          name: "mystery-backend",
          display_name: "Mystery Backend",
          available: false,
        }),
      ]);

      const { result } = renderHook(() => useBackends());

      await flushPromises();

      expect(result.current.backends).toHaveLength(1);
      const backend = result.current.backends[0];
      expect(backend.name).toBe("mystery-backend");
      expect(backend.displayName).toBe("Mystery Backend");
      expect(backend.available).toBe(false);
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

      // Setup new data for refetch
      mockFetchBackends.mockResolvedValueOnce(updatedData);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

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
  });
});
