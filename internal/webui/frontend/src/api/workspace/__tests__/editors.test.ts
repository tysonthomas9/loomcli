/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the editors API functions (editors.ts).
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import type { EditorInfo } from "@/types/common";

// Mock the API client module
vi.mock("@/api/common", () => ({
  get: vi.fn(),
  post: vi.fn(),
  api: {
    GET: vi.fn(),
    POST: vi.fn(),
    PATCH: vi.fn(),
    PUT: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  apiErrorFromResponse: (error: unknown, response?: Response) => {
    const status = response?.status ?? 0;
    const statusText = response?.statusText ?? "Unknown";
    return new Error(`API Error: ${status} ${statusText}`);
  },
}));

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
let mockApiGet: ReturnType<typeof vi.fn>;
let mockApiPost: ReturnType<typeof vi.fn>;

describe("editors API", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const clientMod = await import("@/api/common");
    mockApiGet = vi.mocked(clientMod.api.GET);
    mockApiPost = vi.mocked(clientMod.api.POST);

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
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: { editors } },
        error: undefined,
        response: new Response(),
      });

      const result = await fetchEditors();

      expect(mockApiGet).toHaveBeenCalledTimes(1);
      expect(mockApiGet).toHaveBeenCalledWith("/api/editors");
      expect(result).toEqual(editors);
      expect(result).toHaveLength(2);
    });

    it("always hits the network (no caching)", async () => {
      const editors = [createMockEditor()];
      mockApiGet.mockResolvedValue({
        data: { success: true, data: { editors } },
        error: undefined,
        response: new Response(),
      });

      await fetchEditors();
      await fetchEditors();

      expect(mockApiGet).toHaveBeenCalledTimes(2);
    });

    it("returns empty array when API returns empty editors list", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: { editors: [] } },
        error: undefined,
        response: new Response(),
      });

      const result = await fetchEditors();

      expect(result).toEqual([]);
      expect(result).toHaveLength(0);
    });

    it("throws on network error from client", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { message: "Network error" },
        response: new Response(null, {
          status: 500,
          statusText: "Network error",
        }),
      });

      await expect(fetchEditors()).rejects.toThrow();
    });

    it("throws on API error from client", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      });

      await expect(fetchEditors()).rejects.toThrow();
    });
  });

  describe("refreshEditors", () => {
    it("calls GET /api/editors (delegates to fetchEditors)", async () => {
      const editors = [createMockEditor()];
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: { editors } },
        error: undefined,
        response: new Response(),
      });

      const result = await refreshEditors();

      expect(mockApiGet).toHaveBeenCalledTimes(1);
      expect(mockApiGet).toHaveBeenCalledWith("/api/editors");
      expect(result).toEqual(editors);
    });

    it("throws on network error during refresh", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { message: "Connection refused" },
        response: new Response(null, {
          status: 500,
          statusText: "Connection refused",
        }),
      });

      await expect(refreshEditors()).rejects.toThrow();
    });
  });

  describe("openInEditor", () => {
    it("calls POST /api/editors/open with correct body", async () => {
      mockApiPost.mockResolvedValueOnce({
        data: { success: true },
        error: undefined,
        response: new Response(),
      });

      await openInEditor("vscode", "/path/to/file.ts");

      expect(mockApiPost).toHaveBeenCalledTimes(1);
      expect(mockApiPost).toHaveBeenCalledWith("/api/editors/open", {
        body: { editor_id: "vscode", path: "/path/to/file.ts" },
      });
    });

    it("passes editor_id and path correctly for different editors", async () => {
      mockApiPost.mockResolvedValueOnce({
        data: { success: true },
        error: undefined,
        response: new Response(),
      });

      await openInEditor("cursor", "/workspace/src/main.go");

      expect(mockApiPost).toHaveBeenCalledWith("/api/editors/open", {
        body: { editor_id: "cursor", path: "/workspace/src/main.go" },
      });
    });

    it("returns void on success", async () => {
      mockApiPost.mockResolvedValueOnce({
        data: { success: true },
        error: undefined,
        response: new Response(),
      });

      const result = await openInEditor("vscode", "/path/to/file.ts");

      expect(result).toBeUndefined();
    });

    it("throws on network error from client", async () => {
      mockApiPost.mockResolvedValueOnce({
        data: undefined,
        error: { message: "Connection refused" },
        response: new Response(null, {
          status: 500,
          statusText: "Connection refused",
        }),
      });

      await expect(
        openInEditor("vscode", "/path/to/file.ts"),
      ).rejects.toThrow();
    });

    it("throws on API error from client", async () => {
      mockApiPost.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      });

      await expect(openInEditor("unknown-editor", "/path")).rejects.toThrow();
    });
  });
});
