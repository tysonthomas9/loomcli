import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  getTaskLogPhases,
  getTaskLogContent,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
  getAgentLogArchive,
} from "../logs";
import { ApiError, api, get } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockGet = vi.mocked(get);

describe("logs API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("getTaskLogPhases", () => {
    it("returns phases on successful response", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { phases: ["planning", "implementation"] },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getTaskLogPhases("test-ws-id", "issue-abc");

      expect(result).toEqual(["planning", "implementation"]);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{id}/logs",
        {
          params: { path: { ws: "test-ws-id", id: "issue-abc" } },
        },
      );
    });

    it("returns empty array on 404", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const result = await getTaskLogPhases("test-ws-id", "nonexistent");

      expect(result).toEqual([]);
    });

    it("throws on non-404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(getTaskLogPhases("test-ws-id", "issue-abc")).rejects.toThrow(
        ApiError,
      );
    });
  });

  describe("getTaskLogContent", () => {
    it("returns log snapshot content on success", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { lines: ["a", "b"], line_count: 2 },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "issue-abc",
        "planning",
        25,
      );
      expect(content).toEqual({ lines: ["a", "b"], lineCount: 2 });
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{id}/logs/{phase}",
        {
          params: {
            path: { ws: "test-ws-id", id: "issue-abc", phase: "planning" },
            query: { lines: 25 },
          },
        },
      );
    });

    it("returns empty content for 404 responses", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "missing",
        "implementation",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });

    it("throws on non-404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        getTaskLogContent("test-ws-id", "issue-abc", "planning"),
      ).rejects.toThrow(ApiError);
    });

    it("normalizes missing data fields", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { lines: null, line_count: null },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "issue-abc",
        "planning",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });
  });

  describe("agent terminal endpoints", () => {
    it("fetches agent terminal mode (legacy get)", async () => {
      mockGet.mockResolvedValueOnce({
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
      mockApiGet.mockResolvedValueOnce({
        data: { token: "abc123" },
        error: undefined,
        response: new Response(),
      } as never);

      const token = await getAgentTerminalToken("test-ws-id", "ember");

      expect(token).toBe("abc123");
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/agents/{name}/terminal/token",
        {
          params: { path: { ws: "test-ws-id", name: "ember" } },
        },
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
      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: { lines: ["a", "b"], line_count: 2, start_line: 1 },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const archive = await getAgentLogArchive("test-ws-id", "ember", 100);

      expect(archive).toEqual({
        lines: ["a", "b"],
        lineCount: 2,
        startLine: 1,
      });
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/agents/{name}/logs",
        {
          params: {
            path: { ws: "test-ws-id", name: "ember" },
            query: { lines: 100 },
          },
        },
      );
    });

    it("returns empty results on 404 (no logs available)", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const archive = await getAgentLogArchive("test-ws-id", "idle-agent", 100);

      expect(archive).toEqual({ lines: [], lineCount: 0, startLine: 1 });
    });

    it("throws on non-404 errors", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        getAgentLogArchive("test-ws-id", "ember", 100),
      ).rejects.toThrow(ApiError);
    });

    it("normalizes null archive payload fields", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: { lines: null, line_count: null, start_line: null },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const archive = await getAgentLogArchive("test-ws-id", "ember", 50);

      expect(archive).toEqual({ lines: [], lineCount: 0, startLine: 1 });
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/agents/{name}/logs",
        {
          params: {
            path: { ws: "test-ws-id", name: "ember" },
            query: { lines: 50 },
          },
        },
      );
    });
  });
});
