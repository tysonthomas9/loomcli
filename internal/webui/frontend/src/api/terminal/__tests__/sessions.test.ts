/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the sessions API module.
 * Covers getTaskSessions, getSession, getSessionTranscript, and getSessionDiff.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, api } from "@/api/common";
import {
  getTaskSessions,
  getSession,
  getSessionTranscript,
  getSessionDiff,
} from "../sessions";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    getText: vi.fn(),
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

describe("sessions API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ============= getTaskSessions =============

  describe("getTaskSessions", () => {
    it.each([null, { session_id: "s", is_active: "false" }])(
      "rejects malformed session records",
      async (item) => {
        mockApiGet.mockResolvedValueOnce({
          data: { success: true, data: { sessions: [item] } },
          response: new Response(),
        } as never);
        await expect(getTaskSessions("ws", "task")).rejects.toThrow(
          "Invalid task sessions response",
        );
      },
    );

    it("returns sessions from response data", async () => {
      const sessions = [
        {
          session_id: "s1",
          agent_name: "ember",
          backend: "claude",
          status: "completed",
          started_at: "2026-01-01T00:00:00Z",
          input_tokens: 100,
          output_tokens: 50,
          cache_read_tokens: 0,
          cache_write_tokens: 0,
          estimated_cost_usd: 0.01,
          exit_code: 0,
          files_changed: 1,
          lines_added: 10,
          lines_removed: 5,
          attempt_num: 1,
          has_transcript: true,
          has_diff: true,
          is_active: false,
        },
      ];

      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: { sessions },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getTaskSessions("test-ws-id", "loom-abc123");

      expect(result).toEqual(sessions);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions",
        {
          params: { path: { ws: "test-ws-id", taskId: "loom-abc123" } },
        },
      );
    });

    it("rejects missing session data", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { data: null },
        error: undefined,
        response: new Response(),
      } as never);

      await expect(
        getTaskSessions("test-ws-id", "loom-abc123"),
      ).rejects.toThrow("Invalid task sessions response");
    });

    it("rejects a null session list", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { sessions: null },
        },
        error: undefined,
        response: new Response(),
      } as never);

      await expect(
        getTaskSessions("test-ws-id", "loom-abc123"),
      ).rejects.toThrow("Invalid task sessions response");
    });

    it("propagates 404 instead of acknowledging an empty snapshot", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      await expect(
        getTaskSessions("test-ws-id", "loom-nonexistent"),
      ).rejects.toThrow(ApiError);
    });

    it("throws on non-404 API errors", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(getTaskSessions("test-ws-id", "loom-err")).rejects.toThrow(
        ApiError,
      );
    });

    it("throws on network errors", async () => {
      mockApiGet.mockRejectedValueOnce(new Error("Network error"));

      await expect(getTaskSessions("test-ws-id", "loom-err")).rejects.toThrow(
        "Network error",
      );
    });

    it("passes workspace and task IDs as path params", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: { sessions: [] } },
        error: undefined,
        response: new Response(),
      } as never);

      await getTaskSessions("test-ws-id", "task with spaces");

      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions",
        {
          params: {
            path: { ws: "test-ws-id", taskId: "task with spaces" },
          },
        },
      );
    });
  });

  // ============= getSession =============

  describe("getSession", () => {
    it("returns session record from response data", async () => {
      const session = {
        id: "s1",
        agent_name: "ember",
        backend: "claude",
        status: "running",
        started_at: "2026-01-01T00:00:00Z",
        input_tokens: 200,
        output_tokens: 100,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
        estimated_cost_usd: 0.02,
        exit_code: 0,
        files_changed: 0,
        lines_added: 0,
        lines_removed: 0,
        attempt_num: 1,
        has_transcript: false,
        has_diff: false,
        is_active: true,
      };

      mockApiGet.mockResolvedValueOnce({
        data: { data: session },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSession("test-ws-id", "loom-123", "s1");

      expect(result).toEqual(session);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}",
        {
          params: {
            path: { ws: "test-ws-id", taskId: "loom-123", sessionId: "s1" },
          },
        },
      );
    });

    it("returns null when data is null", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { data: null },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSession("test-ws-id", "loom-123", "s-missing");

      expect(result).toBeNull();
    });

    it("returns null on 404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const result = await getSession(
        "test-ws-id",
        "loom-123",
        "s-nonexistent",
      );

      expect(result).toBeNull();
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

      await expect(getSession("test-ws-id", "loom-123", "s1")).rejects.toThrow(
        ApiError,
      );
    });

    it("passes all IDs as path params", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { data: null },
        error: undefined,
        response: new Response(),
      } as never);

      await getSession("test-ws-id", "task/id", "session/id");

      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}",
        {
          params: {
            path: {
              ws: "test-ws-id",
              taskId: "task/id",
              sessionId: "session/id",
            },
          },
        },
      );
    });
  });

  // ============= getSessionTranscript =============

  describe("getSessionTranscript", () => {
    it("returns transcript entries from response data", async () => {
      const entries = [
        {
          seq: 1,
          ts: "2026-01-01T00:00:00Z",
          role: "user",
          type: "text",
          content: "Hello",
        },
        {
          seq: 2,
          ts: "2026-01-01T00:00:01Z",
          role: "assistant",
          type: "text",
          content: "Hi there",
        },
      ];

      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { entries },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSessionTranscript("test-ws-id", "loom-123", "s1");

      expect(result).toEqual(entries);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript",
        {
          params: {
            path: { ws: "test-ws-id", taskId: "loom-123", sessionId: "s1" },
          },
        },
      );
    });

    it("returns empty array when data is null", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { data: null },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSessionTranscript("test-ws-id", "loom-123", "s1");

      expect(result).toEqual([]);
    });

    it("returns empty array when entries is null", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { entries: null },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSessionTranscript("test-ws-id", "loom-123", "s1");

      expect(result).toEqual([]);
    });

    it("returns empty array on 404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const result = await getSessionTranscript(
        "test-ws-id",
        "loom-123",
        "s-missing",
      );

      expect(result).toEqual([]);
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
        getSessionTranscript("test-ws-id", "loom-123", "s1"),
      ).rejects.toThrow(ApiError);
    });

    it("passes all IDs as path params", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { data: { entries: [] } },
        error: undefined,
        response: new Response(),
      } as never);

      await getSessionTranscript("test-ws-id", "task id", "session id");

      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript",
        {
          params: {
            path: {
              ws: "test-ws-id",
              taskId: "task id",
              sessionId: "session id",
            },
          },
        },
      );
    });
  });

  // ============= getSessionDiff =============

  describe("getSessionDiff", () => {
    it("returns diff text on success", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: "diff --git a/file.ts b/file.ts\n+added line\n",
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSessionDiff("test-ws-id", "loom-123", "s1");

      expect(result).toBe("diff --git a/file.ts b/file.ts\n+added line\n");
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff",
        {
          params: {
            path: { ws: "test-ws-id", taskId: "loom-123", sessionId: "s1" },
          },
          parseAs: "text",
        },
      );
    });

    it("returns null on 404", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const result = await getSessionDiff(
        "test-ws-id",
        "loom-123",
        "s-missing",
      );

      expect(result).toBeNull();
    });

    it("throws ApiError on non-404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        getSessionDiff("test-ws-id", "loom-123", "s1"),
      ).rejects.toThrow(ApiError);
    });

    it("returns empty string for empty diff", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: "",
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getSessionDiff("test-ws-id", "loom-123", "s1");

      // Note: empty string is falsy, so getSessionDiff returns null for it
      // based on the implementation: `return data ?? null;`
      // An empty string is not nullish, so it should return ""
      expect(result).toBe("");
    });

    it("passes all IDs as path params", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: "",
        error: undefined,
        response: new Response(),
      } as never);

      await getSessionDiff("test-ws-id", "task/id", "session/id");

      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff",
        {
          params: {
            path: {
              ws: "test-ws-id",
              taskId: "task/id",
              sessionId: "session/id",
            },
          },
          parseAs: "text",
        },
      );
    });
  });
});
