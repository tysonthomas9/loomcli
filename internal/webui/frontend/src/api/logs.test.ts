import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  getTaskLogPhases,
  getTaskLogContent,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
  getAgentLogArchive,
} from "./logs";
import { ApiError, get } from "./client";

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return {
    ...actual,
    get: vi.fn(),
  };
});

const mockGet = get as ReturnType<typeof vi.fn>;

describe("logs API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("getTaskLogPhases", () => {
    it("returns phases on successful response", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { phases: ["planning", "implementation"] },
      });

      const result = await getTaskLogPhases("test-ws-id", "beads-abc");

      expect(result).toEqual(["planning", "implementation"]);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/tasks/beads-abc/logs",
      );
    });

    it("returns empty array on 404", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const result = await getTaskLogPhases("test-ws-id", "nonexistent");

      expect(result).toEqual([]);
    });

    it("throws on non-404 error", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(getTaskLogPhases("beads-abc")).rejects.toThrow(ApiError);
    });
  });

  describe("getTaskLogContent", () => {
    it("returns log snapshot content on success", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: ["a", "b"], line_count: 2 },
      });

      const content = await getTaskLogContent(
        "test-ws-id",
        "beads-abc",
        "planning",
        25,
      );
      expect(content).toEqual({ lines: ["a", "b"], lineCount: 2 });
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/tasks/beads-abc/logs/planning?lines=25",
      );
    });

    it("returns empty content for 404 responses", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const content = await getTaskLogContent(
        "test-ws-id",
        "missing",
        "implementation",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });

    it("throws on non-404 error", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(getTaskLogContent("beads-abc", "planning")).rejects.toThrow(
        ApiError,
      );
    });

    it("normalizes missing data fields", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: null, line_count: null },
      });

      const content = await getTaskLogContent(
        "test-ws-id",
        "beads-abc",
        "planning",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });
  });

  describe("agent terminal endpoints", () => {
    it("fetches agent terminal mode", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { agent: "ember", mode: "tmux" },
      });

      const mode = await getAgentTerminalInfo("test-ws-id", "ember");

      expect(mode).toBe("tmux");
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/terminal/info",
      );
    });

    it("fetches one-time agent terminal token", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { token: "abc123" },
      });

      const token = await getAgentTerminalToken("test-ws-id", "ember");

      expect(token).toBe("abc123");
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/terminal/token",
      );
    });

    it("builds ws url for agent terminal", () => {
      const url = getAgentTerminalWsUrl("test-ws-id", "ember", "abc123");
      expect(url).toContain(
        "/api/workspaces/test-ws-id/agents/ember/terminal/ws?token=abc123",
      );
      expect(url.startsWith("ws://") || url.startsWith("wss://")).toBe(true);
    });

    it("fetches static agent archive logs", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: ["a", "b"], line_count: 2, start_line: 1 },
      });

      const archive = await getAgentLogArchive("test-ws-id", "ember", 100);

      expect(archive).toEqual({
        lines: ["a", "b"],
        lineCount: 2,
        startLine: 1,
      });
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/logs?lines=100",
      );
    });

    it("returns empty results on 404 (no logs available)", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const archive = await getAgentLogArchive("test-ws-id", "idle-agent", 100);

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

      const archive = await getAgentLogArchive("test-ws-id", "ember", 50);

      expect(archive).toEqual({ lines: [], lineCount: 0, startLine: 1 });
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/logs?lines=50",
      );
    });
  });
});
