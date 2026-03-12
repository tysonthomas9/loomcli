/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the editors API functions (editors.ts).
 *
 * These tests verify that fetchEditors, refreshEditors, getCachedEditors,
 * and openInEditor correctly call the API client, manage the module-level
 * cache, and propagate errors.
 *
 * Because editors.ts uses a module-level cache (editorCache), we use
 * vi.resetModules() + dynamic import in beforeEach to get a fresh module
 * instance for each test.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import type { EditorInfo } from "@/types/editor";

// Mock the API client module
vi.mock("../client", () => ({
  get: vi.fn(),
  post: vi.fn(),
}));

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
 * Because the editors module has a module-level cache, we dynamically import
 * a fresh copy of the module (and its client dependency) in each test via
 * vi.resetModules(). The helpers below are reassigned in beforeEach.
 */
let fetchEditors: typeof import("../editors").fetchEditors;
let refreshEditors: typeof import("../editors").refreshEditors;
let getCachedEditors: typeof import("../editors").getCachedEditors;
let openInEditor: typeof import("../editors").openInEditor;
let mockGet: ReturnType<typeof vi.fn>;
let mockPost: ReturnType<typeof vi.fn>;

describe("editors API", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    // Re-import after resetModules to get a fresh module with empty cache
    const clientMod = await import("../client");
    mockGet = vi.mocked(clientMod.get);
    mockPost = vi.mocked(clientMod.post);

    const editorsMod = await import("../editors");
    fetchEditors = editorsMod.fetchEditors;
    refreshEditors = editorsMod.refreshEditors;
    getCachedEditors = editorsMod.getCachedEditors;
    openInEditor = editorsMod.openInEditor;
  });

  describe("fetchEditors", () => {
    it("calls GET /api/editors and returns editors array", async () => {
      const editors = [
        createMockEditor(),
        createMockEditor({
          id: "cursor",
          display_name: "Cursor",
          detected: false,
        }),
      ];
      mockGet.mockResolvedValueOnce({ editors });

      const result = await fetchEditors();

      expect(mockGet).toHaveBeenCalledTimes(1);
      expect(mockGet).toHaveBeenCalledWith("/api/editors");
      expect(result).toEqual(editors);
      expect(result).toHaveLength(2);
    });

    it("returns cached data on second call without making another HTTP request", async () => {
      const editors = [createMockEditor()];
      mockGet.mockResolvedValueOnce({ editors });

      const first = await fetchEditors();
      const second = await fetchEditors();

      expect(mockGet).toHaveBeenCalledTimes(1);
      expect(first).toEqual(editors);
      expect(second).toEqual(editors);
      expect(first).toBe(second);
    });

    it("returns empty array when API returns empty editors list", async () => {
      mockGet.mockResolvedValueOnce({ editors: [] });

      const result = await fetchEditors();

      expect(result).toEqual([]);
      expect(result).toHaveLength(0);
    });

    it("throws on network error from client", async () => {
      mockGet.mockRejectedValueOnce(new Error("Network error"));

      await expect(fetchEditors()).rejects.toThrow("Network error");
    });

    it("throws on API error from client", async () => {
      mockGet.mockRejectedValueOnce(
        new Error("API Error: 500 Internal Server Error"),
      );

      await expect(fetchEditors()).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  describe("refreshEditors", () => {
    it("invalidates cache and re-fetches from API", async () => {
      const initialEditors = [
        createMockEditor({ id: "vscode", detected: true }),
      ];
      const updatedEditors = [
        createMockEditor({ id: "vscode", detected: true }),
        createMockEditor({
          id: "cursor",
          display_name: "Cursor",
          detected: true,
        }),
      ];
      mockGet.mockResolvedValueOnce({ editors: initialEditors });
      mockGet.mockResolvedValueOnce({ editors: updatedEditors });

      // First fetch populates cache
      const first = await fetchEditors();
      expect(first).toEqual(initialEditors);
      expect(mockGet).toHaveBeenCalledTimes(1);

      // Refresh invalidates cache and re-fetches
      const refreshed = await refreshEditors();
      expect(refreshed).toEqual(updatedEditors);
      expect(mockGet).toHaveBeenCalledTimes(2);
    });

    it("updates the cache so subsequent fetchEditors returns new data", async () => {
      const initialEditors = [createMockEditor()];
      const updatedEditors = [
        createMockEditor({
          id: "neovim",
          display_name: "Neovim",
          detected: false,
        }),
      ];
      mockGet.mockResolvedValueOnce({ editors: initialEditors });
      mockGet.mockResolvedValueOnce({ editors: updatedEditors });

      await fetchEditors();
      await refreshEditors();

      // Third call should use the refreshed cache, no new HTTP request
      const result = await fetchEditors();
      expect(result).toEqual(updatedEditors);
      expect(mockGet).toHaveBeenCalledTimes(2);
    });

    it("throws on network error during refresh", async () => {
      mockGet.mockRejectedValueOnce(new Error("Connection refused"));

      await expect(refreshEditors()).rejects.toThrow("Connection refused");
    });
  });

  describe("getCachedEditors", () => {
    it("returns null before any fetch has been made", () => {
      const result = getCachedEditors();

      expect(result).toBeNull();
    });

    it("returns editors array after fetchEditors has been called", async () => {
      const editors = [
        createMockEditor(),
        createMockEditor({
          id: "intellij",
          display_name: "IntelliJ",
          detected: false,
        }),
      ];
      mockGet.mockResolvedValueOnce({ editors });

      await fetchEditors();
      const cached = getCachedEditors();

      expect(cached).toEqual(editors);
      expect(cached).toHaveLength(2);
    });

    it("returns updated data after refreshEditors", async () => {
      const initial = [createMockEditor({ id: "vscode" })];
      const updated = [
        createMockEditor({ id: "cursor", display_name: "Cursor" }),
      ];
      mockGet.mockResolvedValueOnce({ editors: initial });
      mockGet.mockResolvedValueOnce({ editors: updated });

      await fetchEditors();
      expect(getCachedEditors()).toEqual(initial);

      await refreshEditors();
      expect(getCachedEditors()).toEqual(updated);
    });
  });

  describe("openInEditor", () => {
    it("calls POST /api/editors/open with correct body", async () => {
      mockPost.mockResolvedValueOnce({ success: true });

      await openInEditor("vscode", "/path/to/file.ts");

      expect(mockPost).toHaveBeenCalledTimes(1);
      expect(mockPost).toHaveBeenCalledWith("/api/editors/open", {
        editor_id: "vscode",
        path: "/path/to/file.ts",
      });
    });

    it("passes editor_id and path correctly for different editors", async () => {
      mockPost.mockResolvedValueOnce({ success: true });

      await openInEditor("cursor", "/workspace/src/main.go");

      expect(mockPost).toHaveBeenCalledWith("/api/editors/open", {
        editor_id: "cursor",
        path: "/workspace/src/main.go",
      });
    });

    it("returns void on success", async () => {
      mockPost.mockResolvedValueOnce({ success: true });

      const result = await openInEditor("vscode", "/path/to/file.ts");

      expect(result).toBeUndefined();
    });

    it("throws on network error from client", async () => {
      mockPost.mockRejectedValueOnce(new Error("Connection refused"));

      await expect(openInEditor("vscode", "/path/to/file.ts")).rejects.toThrow(
        "Connection refused",
      );
    });

    it("throws on API error from client", async () => {
      mockPost.mockRejectedValueOnce(new Error("API Error: 404 Not Found"));

      await expect(openInEditor("unknown-editor", "/path")).rejects.toThrow(
        "API Error: 404 Not Found",
      );
    });
  });
});
