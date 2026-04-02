/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the editors API functions (editors.ts).
 *
 * These tests verify that fetchEditors, refreshEditors, and openInEditor
 * correctly call the API client. The module is now stateless — caching
 * lives in editorStore, so there are no cache tests here.
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

let fetchEditors: typeof import("../editors").fetchEditors;
let refreshEditors: typeof import("../editors").refreshEditors;
let openInEditor: typeof import("../editors").openInEditor;
let mockGet: ReturnType<typeof vi.fn>;
let mockPost: ReturnType<typeof vi.fn>;

describe("editors API", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const clientMod = await import("../client");
    mockGet = vi.mocked(clientMod.get);
    mockPost = vi.mocked(clientMod.post);

    const editorsMod = await import("../editors");
    fetchEditors = editorsMod.fetchEditors;
    refreshEditors = editorsMod.refreshEditors;
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

    it("always hits the network (no caching)", async () => {
      const editors = [createMockEditor()];
      mockGet.mockResolvedValue({ editors });

      await fetchEditors();
      await fetchEditors();

      expect(mockGet).toHaveBeenCalledTimes(2);
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
    it("calls GET /api/editors (delegates to fetchEditors)", async () => {
      const editors = [createMockEditor()];
      mockGet.mockResolvedValueOnce({ editors });

      const result = await refreshEditors();

      expect(mockGet).toHaveBeenCalledTimes(1);
      expect(mockGet).toHaveBeenCalledWith("/api/editors");
      expect(result).toEqual(editors);
    });

    it("throws on network error during refresh", async () => {
      mockGet.mockRejectedValueOnce(new Error("Connection refused"));

      await expect(refreshEditors()).rejects.toThrow("Connection refused");
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
