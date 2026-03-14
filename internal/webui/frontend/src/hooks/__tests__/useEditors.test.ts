/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useEditors hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * cache-warm fast path, detectedEditors filtering, refresh, and openEditor.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  fetchEditors,
  refreshEditors,
  getCachedEditors,
  openInEditor,
} from "@/api/editors";
import type { EditorInfo } from "@/types/editor";

import { useEditors } from "../useEditors";

// Mock the editors API module
vi.mock("@/api/editors", () => ({
  fetchEditors: vi.fn(),
  refreshEditors: vi.fn(),
  getCachedEditors: vi.fn(),
  openInEditor: vi.fn(),
}));

const mockFetchEditors = vi.mocked(fetchEditors);
const mockRefreshEditors = vi.mocked(refreshEditors);
const mockGetCachedEditors = vi.mocked(getCachedEditors);
const mockOpenInEditor = vi.mocked(openInEditor);

/**
 * Helper to create a mock EditorInfo.
 */
function createMockEditor(overrides?: Partial<EditorInfo>): EditorInfo {
  return {
    id: "vscode",
    display_name: "VS Code",
    icon_name: "vscode",
    detected: true,
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

describe("useEditors", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Default: no cache, so the hook will fetch on mount
    mockGetCachedEditors.mockReturnValue(null);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial loading state", () => {
    it("returns loading true and empty editors when cache is empty", async () => {
      mockGetCachedEditors.mockReturnValue(null);
      mockFetchEditors.mockResolvedValueOnce([createMockEditor()]);

      const { result } = renderHook(() => useEditors());

      // Initially loading with empty editors
      expect(result.current.isLoading).toBe(true);
      expect(result.current.editors).toEqual([]);
      expect(result.current.error).toBeNull();

      await flushPromises();
    });
  });

  describe("fetch on mount", () => {
    it("fetches editors on mount and updates state", async () => {
      const editors = [
        createMockEditor(),
        createMockEditor({
          id: "cursor",
          display_name: "Cursor",
          detected: false,
        }),
      ];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(mockFetchEditors).toHaveBeenCalledTimes(1);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.editors).toEqual(editors);
      expect(result.current.error).toBeNull();
    });

    it("sets loading false after fetch completes", async () => {
      mockFetchEditors.mockResolvedValueOnce([createMockEditor()]);

      const { result } = renderHook(() => useEditors());

      expect(result.current.isLoading).toBe(true);

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("cache-warm fast path", () => {
    it("returns cached data immediately without loading when cache is warm", async () => {
      const cachedEditors = [
        createMockEditor(),
        createMockEditor({
          id: "intellij",
          display_name: "IntelliJ",
          detected: true,
        }),
      ];
      mockGetCachedEditors.mockReturnValue(cachedEditors);

      const { result } = renderHook(() => useEditors());

      // Should not be loading since cache was warm
      expect(result.current.isLoading).toBe(false);
      expect(result.current.editors).toEqual(cachedEditors);
      expect(result.current.error).toBeNull();

      await flushPromises();

      // Should not have called fetchEditors since cache was available
      expect(mockFetchEditors).not.toHaveBeenCalled();
    });
  });

  describe("detectedEditors", () => {
    it("filters to only include editors with detected: true", async () => {
      const editors = [
        createMockEditor({
          id: "vscode",
          display_name: "VS Code",
          detected: true,
        }),
        createMockEditor({
          id: "cursor",
          display_name: "Cursor",
          detected: false,
        }),
        createMockEditor({
          id: "intellij",
          display_name: "IntelliJ",
          detected: true,
        }),
        createMockEditor({
          id: "neovim",
          display_name: "Neovim",
          detected: false,
        }),
      ];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.editors).toHaveLength(4);
      expect(result.current.detectedEditors).toHaveLength(2);
      expect(result.current.detectedEditors[0].id).toBe("vscode");
      expect(result.current.detectedEditors[1].id).toBe("intellij");
    });

    it("returns empty detectedEditors when no editors are detected", async () => {
      const editors = [
        createMockEditor({ id: "vscode", detected: false }),
        createMockEditor({ id: "cursor", detected: false }),
      ];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.editors).toHaveLength(2);
      expect(result.current.detectedEditors).toHaveLength(0);
    });

    it("returns all editors as detected when all have detected: true", async () => {
      const editors = [
        createMockEditor({ id: "vscode", detected: true }),
        createMockEditor({ id: "cursor", detected: true }),
      ];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.detectedEditors).toHaveLength(2);
      expect(result.current.detectedEditors).toEqual(editors);
    });
  });

  describe("refresh", () => {
    it("triggers re-fetch and updates state", async () => {
      const initialEditors = [createMockEditor({ id: "vscode" })];
      const updatedEditors = [
        createMockEditor({ id: "vscode" }),
        createMockEditor({
          id: "cursor",
          display_name: "Cursor",
          detected: true,
        }),
      ];
      mockFetchEditors.mockResolvedValueOnce(initialEditors);
      mockRefreshEditors.mockResolvedValueOnce(updatedEditors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.editors).toEqual(initialEditors);

      await act(async () => {
        await result.current.refresh();
      });

      expect(mockRefreshEditors).toHaveBeenCalledTimes(1);
      expect(result.current.editors).toEqual(updatedEditors);
      expect(result.current.isLoading).toBe(false);
    });

    it("sets loading true during refresh and false after", async () => {
      const editors = [createMockEditor()];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);

      let resolveRefresh!: (value: EditorInfo[]) => void;
      mockRefreshEditors.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveRefresh = resolve;
          }),
      );

      let refreshPromise: Promise<void>;
      act(() => {
        refreshPromise = result.current.refresh();
      });

      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveRefresh([createMockEditor()]);
        await refreshPromise!;
      });

      expect(result.current.isLoading).toBe(false);
    });

    it("clears previous error on successful refresh", async () => {
      // Initial fetch fails
      mockFetchEditors.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Network error");

      // Refresh succeeds
      const editors = [createMockEditor()];
      mockRefreshEditors.mockResolvedValueOnce(editors);

      await act(async () => {
        await result.current.refresh();
      });

      expect(result.current.error).toBeNull();
      expect(result.current.editors).toEqual(editors);
    });

    it("sets error on refresh failure", async () => {
      const editors = [createMockEditor()];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      mockRefreshEditors.mockRejectedValueOnce(new Error("Refresh failed"));

      await act(async () => {
        await result.current.refresh();
      });

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Refresh failed");
      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("error state", () => {
    it("sets error on initial fetch failure", async () => {
      mockFetchEditors.mockRejectedValueOnce(new Error("Server unavailable"));

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Server unavailable");
      expect(result.current.editors).toEqual([]);
    });

    it("wraps non-Error exceptions in an Error", async () => {
      mockFetchEditors.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });
  });

  describe("openEditor", () => {
    it("calls openInEditor with correct arguments", async () => {
      const editors = [createMockEditor()];
      mockFetchEditors.mockResolvedValueOnce(editors);
      mockOpenInEditor.mockResolvedValueOnce(undefined);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      await act(async () => {
        await result.current.openEditor("vscode", "/path/to/file.ts");
      });

      expect(mockOpenInEditor).toHaveBeenCalledTimes(1);
      expect(mockOpenInEditor).toHaveBeenCalledWith(
        "vscode",
        "/path/to/file.ts",
      );
    });

    it("propagates errors from openInEditor", async () => {
      const editors = [createMockEditor()];
      mockFetchEditors.mockResolvedValueOnce(editors);
      mockOpenInEditor.mockRejectedValueOnce(new Error("Editor not found"));

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      await expect(
        act(async () => {
          await result.current.openEditor("unknown", "/path");
        }),
      ).rejects.toThrow("Editor not found");
    });
  });

  describe("unmount safety", () => {
    it("does not update state after unmount during fetch", async () => {
      let resolveFetch!: (value: EditorInfo[]) => void;
      mockFetchEditors.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { unmount } = renderHook(() => useEditors());

      // Unmount before fetch resolves
      unmount();

      // Resolve the fetch — should not throw
      await act(async () => {
        resolveFetch([createMockEditor()]);
        await Promise.resolve();
      });

      // If we got here without errors, the test passes
    });

    it("does not update state after unmount during refresh", async () => {
      const editors = [createMockEditor()];
      mockFetchEditors.mockResolvedValueOnce(editors);

      const { result, unmount } = renderHook(() => useEditors());

      await flushPromises();

      let resolveRefresh!: (value: EditorInfo[]) => void;
      mockRefreshEditors.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveRefresh = resolve;
          }),
      );

      let refreshPromise: Promise<void>;
      act(() => {
        refreshPromise = result.current.refresh();
      });

      // Unmount before refresh resolves
      unmount();

      // Resolve the refresh — should not throw
      await act(async () => {
        resolveRefresh([createMockEditor({ id: "cursor" })]);
        await refreshPromise!;
      });

      // If we got here without errors, the test passes
    });
  });
});
