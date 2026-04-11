/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useEditors hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * detectedEditors filtering, refresh, and openEditor.
 * The hook delegates to editorStore (Zustand vanilla store).
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { EditorInfo } from "@/types/common";
import { openInEditor } from "@/api/editors";

// Mock the editors API module (used by the store internally and directly by the hook)
vi.mock("@/api/editors", () => ({
  fetchEditors: vi.fn(),
  openInEditor: vi.fn(),
}));

// We need to mock @/stores so we can provide a fresh editorStore per test
vi.mock("@/stores", async (importOriginal) => {
  const original = (await importOriginal()) as typeof import("@/stores");
  return {
    ...original,
    // editorStore will be overridden in beforeEach
    editorStore: original.createEditorStore(),
  };
});

import { editorStore } from "@/stores";
import { fetchEditors as apiFetchEditors } from "@/api/editors";

const mockApiFetchEditors = vi.mocked(apiFetchEditors);
const mockOpenInEditor = vi.mocked(openInEditor);

// Import the hook after mocks are set up
import { useEditors } from "../useEditors";

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
    // Reset the store state between tests
    editorStore.getState().reset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial loading state", () => {
    it("returns empty editors and no error initially", async () => {
      mockApiFetchEditors.mockResolvedValueOnce([createMockEditor()]);

      const { result } = renderHook(() => useEditors());

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
      mockApiFetchEditors.mockResolvedValueOnce(editors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(mockApiFetchEditors).toHaveBeenCalledTimes(1);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.editors).toEqual(editors);
      expect(result.current.error).toBeNull();
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
      mockApiFetchEditors.mockResolvedValueOnce(editors);

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
      mockApiFetchEditors.mockResolvedValueOnce(editors);

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
      mockApiFetchEditors.mockResolvedValueOnce(editors);

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
      mockApiFetchEditors.mockResolvedValueOnce(initialEditors);

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.editors).toEqual(initialEditors);

      // refreshEditors on the store calls the API's fetchEditors again
      mockApiFetchEditors.mockResolvedValueOnce(updatedEditors);

      await act(async () => {
        await result.current.refresh();
      });

      expect(result.current.editors).toEqual(updatedEditors);
      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("error state", () => {
    it("sets error on initial fetch failure", async () => {
      mockApiFetchEditors.mockRejectedValueOnce(
        new Error("Server unavailable"),
      );

      const { result } = renderHook(() => useEditors());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Server unavailable");
      expect(result.current.editors).toEqual([]);
    });
  });

  describe("openEditor", () => {
    it("calls openInEditor with correct arguments", async () => {
      mockApiFetchEditors.mockResolvedValueOnce([createMockEditor()]);
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
      mockApiFetchEditors.mockResolvedValueOnce([createMockEditor()]);
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
});
