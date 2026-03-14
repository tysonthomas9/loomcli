import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  getTaskLogPhases,
  getTaskLogContent,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
  getAgentLogArchive,
} from "./logs";
import { ApiError, getAuthToken, get } from "./client";

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return {
    ...actual,
    getAuthToken: vi.fn(),
    get: vi.fn(),
  };
});

const mockGetAuthToken = getAuthToken as ReturnType<typeof vi.fn>;
const mockGet = get as ReturnType<typeof vi.fn>;

describe("logs API", () => {
  let originalFetch: typeof global.fetch;

  beforeEach(() => {
    originalFetch = global.fetch;
    vi.clearAllMocks();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  describe("getTaskLogPhases", () => {
    it("returns phases on successful response", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () =>
          Promise.resolve({
            success: true,
            data: { phases: ["planning", "implementation"] },
          }),
      });

      const result = await getTaskLogPhases("beads-abc");

      expect(result).toEqual(["planning", "implementation"]);
    });

    it("returns empty array on 404", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
      });

      const result = await getTaskLogPhases("nonexistent");

      expect(result).toEqual([]);
    });

    it("throws on non-404 error", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
      });

      await expect(getTaskLogPhases("beads-abc")).rejects.toThrow(
        "Failed to fetch log phases",
      );
    });

    it("includes Authorization header when token exists", async () => {
      mockGetAuthToken.mockReturnValue("test-token");
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: [] } }),
      });

      await getTaskLogPhases("beads-abc");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toHaveProperty(
        "Authorization",
        "Bearer test-token",
      );
    });

    it("omits Authorization header when no token", async () => {
      mockGetAuthToken.mockReturnValue(null);
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: [] } }),
      });

      await getTaskLogPhases("beads-abc");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).not.toHaveProperty("Authorization");
    });
  });

  describe("getTaskLogContent", () => {
    it("returns log snapshot content on success", async () => {
      mockGetAuthToken.mockReturnValue("test-token");
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () =>
          Promise.resolve({
            success: true,
            data: { lines: ["a", "b"], line_count: 2 },
          }),
      });

      const content = await getTaskLogContent("beads-abc", "planning", 25);
      expect(content).toEqual({ lines: ["a", "b"], lineCount: 2 });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const [url, options] = mockFn.mock.calls[0] as [
        string,
        { headers: Record<string, string> },
      ];
      expect(url).toBe("/api/tasks/beads-abc/logs/planning?lines=25");
      expect(options.headers.Authorization).toBe("Bearer test-token");
    });

    it("returns empty content for 404 responses", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
      });

      const content = await getTaskLogContent("missing", "implementation");
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });

    it("throws on non-404 error", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
      });

      await expect(getTaskLogContent("beads-abc", "planning")).rejects.toThrow(
        "Failed to fetch task logs",
      );
    });
  });

  describe("agent terminal endpoints", () => {
    it("fetches agent terminal mode", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { agent: "ember", mode: "tmux" },
      });

      const mode = await getAgentTerminalInfo("ember");

      expect(mode).toBe("tmux");
      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/terminal/info");
    });

    it("fetches one-time agent terminal token", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { token: "abc123" },
      });

      const token = await getAgentTerminalToken("ember");

      expect(token).toBe("abc123");
      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/terminal/token");
    });

    it("builds ws url for agent terminal", () => {
      const url = getAgentTerminalWsUrl("ember", "abc123");
      expect(url).toContain("/api/agents/ember/terminal/ws?token=abc123");
      expect(url.startsWith("ws://") || url.startsWith("wss://")).toBe(true);
    });

    it("fetches static agent archive logs", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: ["a", "b"], line_count: 2, start_line: 1 },
      });

      const archive = await getAgentLogArchive("ember", 100);

      expect(archive).toEqual({
        lines: ["a", "b"],
        lineCount: 2,
        startLine: 1,
      });
      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/logs?lines=100");
    });

    it("returns empty results on 404 (no logs available)", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const archive = await getAgentLogArchive("idle-agent", 100);

      expect(archive).toEqual({ lines: [], lineCount: 0, startLine: 1 });
    });

    it("throws on non-404 errors", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(getAgentLogArchive("ember", 100)).rejects.toThrow(
        "API Error: 500",
      );
    });

    it("normalizes null archive payload fields", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: null, line_count: null, start_line: null },
      });

      const archive = await getAgentLogArchive("ember", 50);

      expect(archive).toEqual({ lines: [], lineCount: 0, startLine: 1 });
      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/logs?lines=50");
    });
  });
});
