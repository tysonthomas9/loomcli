/**
 * Unit tests for backendsStore.
 * All tests use the vanilla store directly — no React rendering needed.
 * Mocks @/api/backends for HTTP calls.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import type { StoreApi } from "zustand/vanilla";
import type { BackendsStore } from "../backendsStore";
import type { BackendHealthData } from "../../api/workspace";

// Mock the backends API module
vi.mock(import("../../api/workspace"), async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    fetchBackends: vi.fn(),
  };
});

import { fetchBackends as apiFetchBackends } from "../../api/workspace";
import { createBackendsStore, INITIAL_BACKENDS_STATE } from "../backendsStore";

const mockApiFetchBackends = vi.mocked(apiFetchBackends);

function makeBackend(
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

describe("backendsStore", () => {
  let store: StoreApi<BackendsStore>;

  beforeEach(() => {
    vi.clearAllMocks();
    store = createBackendsStore();
  });

  describe("initial state", () => {
    it("has empty backends, isLoading false, error null", () => {
      const state = store.getState();
      expect(state.backends).toEqual([]);
      expect(state.isLoading).toBe(false);
      expect(state.error).toBeNull();
    });
  });

  describe("fetchBackends", () => {
    it("makes HTTP request on first call and populates backends", async () => {
      const backends = [makeBackend(), makeBackend({ name: "codex" })];
      mockApiFetchBackends.mockResolvedValueOnce(backends);

      const result = await store.getState().fetchBackends();

      expect(mockApiFetchBackends).toHaveBeenCalledTimes(1);
      expect(result).toEqual(backends);
      expect(store.getState().backends).toEqual(backends);
      expect(store.getState().isLoading).toBe(false);
      expect(store.getState().error).toBeNull();
    });

    it("returns cached result without HTTP request on second call", async () => {
      const backends = [makeBackend()];
      mockApiFetchBackends.mockResolvedValueOnce(backends);

      await store.getState().fetchBackends();
      const result = await store.getState().fetchBackends();

      expect(mockApiFetchBackends).toHaveBeenCalledTimes(1);
      expect(result).toEqual(backends);
    });

    it("deduplicates concurrent calls — only one HTTP request", async () => {
      const backends = [makeBackend()];
      mockApiFetchBackends.mockResolvedValueOnce(backends);

      const [r1, r2] = await Promise.all([
        store.getState().fetchBackends(),
        store.getState().fetchBackends(),
      ]);

      expect(mockApiFetchBackends).toHaveBeenCalledTimes(1);
      expect(r1).toEqual(backends);
      expect(r2).toEqual(backends);
    });

    it("sets error on failure and re-throws", async () => {
      mockApiFetchBackends.mockRejectedValueOnce(new Error("Network error"));

      await expect(store.getState().fetchBackends()).rejects.toThrow(
        "Network error",
      );

      expect(store.getState().error).toBe("Network error");
      expect(store.getState().isLoading).toBe(false);
    });
  });

  describe("refreshBackends", () => {
    it("forces a new HTTP request after cache is populated", async () => {
      const initial = [makeBackend({ name: "claude" })];
      const updated = [makeBackend({ name: "codex" })];
      mockApiFetchBackends.mockResolvedValueOnce(initial);
      mockApiFetchBackends.mockResolvedValueOnce(updated);

      await store.getState().fetchBackends();
      const result = await store.getState().refreshBackends();

      expect(mockApiFetchBackends).toHaveBeenCalledTimes(2);
      expect(result).toEqual(updated);
      expect(store.getState().backends).toEqual(updated);
    });

    it("discards stale in-flight response via generation check", async () => {
      let resolveFirst!: (v: BackendHealthData[]) => void;
      mockApiFetchBackends.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          }),
      );

      const fetchPromise = store.getState().fetchBackends();

      const refreshed = [makeBackend({ name: "refreshed" })];
      mockApiFetchBackends.mockResolvedValueOnce(refreshed);

      const refreshPromise = store.getState().refreshBackends();

      // Resolve the stale first fetch
      resolveFirst([makeBackend({ name: "stale" })]);

      await Promise.allSettled([fetchPromise, refreshPromise]);

      expect(store.getState().backends).toEqual(refreshed);
    });
  });

  describe("reset", () => {
    it("clears state to initial and forces next fetch to hit network", async () => {
      const backends = [makeBackend()];
      mockApiFetchBackends.mockResolvedValueOnce(backends);
      await store.getState().fetchBackends();

      store.getState().reset();

      expect(store.getState().backends).toEqual(
        INITIAL_BACKENDS_STATE.backends,
      );
      expect(store.getState().isLoading).toBe(INITIAL_BACKENDS_STATE.isLoading);
      expect(store.getState().error).toBe(INITIAL_BACKENDS_STATE.error);

      // Next fetch should hit network
      const newBackends = [makeBackend({ name: "codex" })];
      mockApiFetchBackends.mockResolvedValueOnce(newBackends);

      await store.getState().fetchBackends();
      expect(mockApiFetchBackends).toHaveBeenCalledTimes(2);
      expect(store.getState().backends).toEqual(newBackends);
    });
  });
});
