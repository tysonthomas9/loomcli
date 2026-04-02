/**
 * Unit tests for editorStore.
 * All tests use the vanilla store directly — no React rendering needed.
 * Mocks @/api/editors for HTTP calls.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import type { StoreApi } from "zustand/vanilla";
import type { EditorStore } from "../editorStore";
import type { EditorInfo } from "../../types/editor";

// Mock the editors API module
vi.mock("../../api/editors", () => ({
  fetchEditors: vi.fn(),
}));

import { fetchEditors as apiFetchEditors } from "../../api/editors";
import { createEditorStore, INITIAL_EDITOR_STATE } from "../editorStore";

const mockApiFetchEditors = vi.mocked(apiFetchEditors);

function makeEditor(overrides?: Partial<EditorInfo>): EditorInfo {
  return {
    id: "vscode",
    display_name: "VS Code",
    icon_name: "vscode",
    detected: true,
    ...overrides,
  };
}

describe("editorStore", () => {
  let store: StoreApi<EditorStore>;

  beforeEach(() => {
    vi.clearAllMocks();
    store = createEditorStore();
  });

  describe("initial state", () => {
    it("has empty editors, isLoading false, error null", () => {
      const state = store.getState();
      expect(state.editors).toEqual([]);
      expect(state.isLoading).toBe(false);
      expect(state.error).toBeNull();
    });
  });

  describe("fetchEditors", () => {
    it("makes HTTP request on first call and populates editors", async () => {
      const editors = [makeEditor(), makeEditor({ id: "cursor" })];
      mockApiFetchEditors.mockResolvedValueOnce(editors);

      const result = await store.getState().fetchEditors();

      expect(mockApiFetchEditors).toHaveBeenCalledTimes(1);
      expect(result).toEqual(editors);
      expect(store.getState().editors).toEqual(editors);
      expect(store.getState().isLoading).toBe(false);
      expect(store.getState().error).toBeNull();
    });

    it("returns cached result without HTTP request on second call", async () => {
      const editors = [makeEditor()];
      mockApiFetchEditors.mockResolvedValueOnce(editors);

      await store.getState().fetchEditors();
      const result = await store.getState().fetchEditors();

      expect(mockApiFetchEditors).toHaveBeenCalledTimes(1);
      expect(result).toEqual(editors);
    });

    it("deduplicates concurrent calls — only one HTTP request", async () => {
      const editors = [makeEditor()];
      mockApiFetchEditors.mockResolvedValueOnce(editors);

      const [r1, r2] = await Promise.all([
        store.getState().fetchEditors(),
        store.getState().fetchEditors(),
      ]);

      expect(mockApiFetchEditors).toHaveBeenCalledTimes(1);
      expect(r1).toEqual(editors);
      expect(r2).toEqual(editors);
    });

    it("sets error on failure and re-throws", async () => {
      mockApiFetchEditors.mockRejectedValueOnce(new Error("Network error"));

      await expect(store.getState().fetchEditors()).rejects.toThrow(
        "Network error",
      );

      expect(store.getState().error).toBe("Network error");
      expect(store.getState().isLoading).toBe(false);
    });

    it("retries after error (hasFetched stays false)", async () => {
      mockApiFetchEditors.mockRejectedValueOnce(new Error("fail"));

      await expect(store.getState().fetchEditors()).rejects.toThrow();

      const editors = [makeEditor()];
      mockApiFetchEditors.mockResolvedValueOnce(editors);

      const result = await store.getState().fetchEditors();
      expect(result).toEqual(editors);
      expect(mockApiFetchEditors).toHaveBeenCalledTimes(2);
    });
  });

  describe("refreshEditors", () => {
    it("forces a new HTTP request after cache is populated", async () => {
      const initial = [makeEditor({ id: "vscode" })];
      const updated = [makeEditor({ id: "cursor" })];
      mockApiFetchEditors.mockResolvedValueOnce(initial);
      mockApiFetchEditors.mockResolvedValueOnce(updated);

      await store.getState().fetchEditors();
      const result = await store.getState().refreshEditors();

      expect(mockApiFetchEditors).toHaveBeenCalledTimes(2);
      expect(result).toEqual(updated);
      expect(store.getState().editors).toEqual(updated);
    });

    it("discards in-flight fetch when called during fetch", async () => {
      let resolveFirst!: (v: EditorInfo[]) => void;
      mockApiFetchEditors.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          }),
      );

      const fetchPromise = store.getState().fetchEditors();

      const refreshed = [makeEditor({ id: "new" })];
      mockApiFetchEditors.mockResolvedValueOnce(refreshed);

      const refreshPromise = store.getState().refreshEditors();

      // Resolve the original fetch
      resolveFirst([makeEditor({ id: "old" })]);

      await Promise.allSettled([fetchPromise, refreshPromise]);

      expect(store.getState().editors).toEqual(refreshed);
    });
  });

  describe("reset", () => {
    it("clears state to initial and forces next fetch to hit network", async () => {
      const editors = [makeEditor()];
      mockApiFetchEditors.mockResolvedValueOnce(editors);
      await store.getState().fetchEditors();

      store.getState().reset();

      expect(store.getState().editors).toEqual(INITIAL_EDITOR_STATE.editors);
      expect(store.getState().isLoading).toBe(INITIAL_EDITOR_STATE.isLoading);
      expect(store.getState().error).toBe(INITIAL_EDITOR_STATE.error);

      // Next fetch should hit network
      const newEditors = [makeEditor({ id: "cursor" })];
      mockApiFetchEditors.mockResolvedValueOnce(newEditors);

      await store.getState().fetchEditors();
      expect(mockApiFetchEditors).toHaveBeenCalledTimes(2);
      expect(store.getState().editors).toEqual(newEditors);
    });
  });
});
