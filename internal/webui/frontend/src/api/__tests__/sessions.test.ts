/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the sessions API module.
 * Covers getTaskSessions, getSession, getSessionTranscript, and getSessionDiff.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, get, getAuthToken } from "../client";
import {
  getTaskSessions,
  getSession,
  getSessionTranscript,
  getSessionDiff,
} from "../sessions";

vi.mock("../client", () => ({
  get: vi.fn(),
  getAuthToken: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(
      public status: number,
      public statusText: string,
      public body?: unknown,
    ) {
      super(`API Error: ${status} ${statusText}`);
      this.name = "ApiError";
    }
  },
}));

const mockGet = vi.mocked(get);
const mockGetAuthToken = vi.mocked(getAuthToken);

// Mock global fetch for getSessionDiff (it uses raw fetch)
const mockFetch = vi.fn();
global.fetch = mockFetch;

describe("sessions API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAuthToken.mockReturnValue(null);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ============= getTaskSessions =============

  describe("getTaskSessions", () => {
    it("returns sessions from response data", async () => {
      const sessions = [
        {
          id: "s1",
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

      mockGet.mockResolvedValueOnce({
        data: { sessions },
      });

      const result = await getTaskSessions("bd-abc123");

      expect(result).toEqual(sessions);
      expect(mockGet).toHaveBeenCalledWith("/api/tasks/bd-abc123/sessions");
    });

    it("returns empty array when data is null", async () => {
      mockGet.mockResolvedValueOnce({ data: null });

      const result = await getTaskSessions("bd-abc123");

      expect(result).toEqual([]);
    });

    it("returns empty array when sessions is null", async () => {
      mockGet.mockResolvedValueOnce({
        data: { sessions: null },
      });

      const result = await getTaskSessions("bd-abc123");

      expect(result).toEqual([]);
    });

    it("returns empty array on 404 error", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(404, "Not Found"));

      const result = await getTaskSessions("bd-nonexistent");

      expect(result).toEqual([]);
    });

    it("throws on non-404 API errors", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(500, "Internal Server Error"));

      await expect(getTaskSessions("bd-err")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });

    it("throws on network errors", async () => {
      mockGet.mockRejectedValueOnce(new Error("Network error"));

      await expect(getTaskSessions("bd-err")).rejects.toThrow("Network error");
    });

    it("URL-encodes the task ID", async () => {
      mockGet.mockResolvedValueOnce({ data: { sessions: [] } });

      await getTaskSessions("task with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/tasks/task%20with%20spaces/sessions",
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

      mockGet.mockResolvedValueOnce({ data: session });

      const result = await getSession("bd-123", "s1");

      expect(result).toEqual(session);
      expect(mockGet).toHaveBeenCalledWith("/api/tasks/bd-123/sessions/s1");
    });

    it("returns null when data is null", async () => {
      mockGet.mockResolvedValueOnce({ data: null });

      const result = await getSession("bd-123", "s-missing");

      expect(result).toBeNull();
    });

    it("returns null on 404 error", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(404, "Not Found"));

      const result = await getSession("bd-123", "s-nonexistent");

      expect(result).toBeNull();
    });

    it("throws on non-404 errors", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(500, "Internal Server Error"));

      await expect(getSession("bd-123", "s1")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });

    it("URL-encodes both task and session IDs", async () => {
      mockGet.mockResolvedValueOnce({ data: null });

      await getSession("task/id", "session/id");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/tasks/task%2Fid/sessions/session%2Fid",
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

      mockGet.mockResolvedValueOnce({
        data: { entries },
      });

      const result = await getSessionTranscript("bd-123", "s1");

      expect(result).toEqual(entries);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/tasks/bd-123/sessions/s1/transcript",
      );
    });

    it("returns empty array when data is null", async () => {
      mockGet.mockResolvedValueOnce({ data: null });

      const result = await getSessionTranscript("bd-123", "s1");

      expect(result).toEqual([]);
    });

    it("returns empty array when entries is null", async () => {
      mockGet.mockResolvedValueOnce({
        data: { entries: null },
      });

      const result = await getSessionTranscript("bd-123", "s1");

      expect(result).toEqual([]);
    });

    it("returns empty array on 404 error", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(404, "Not Found"));

      const result = await getSessionTranscript("bd-123", "s-missing");

      expect(result).toEqual([]);
    });

    it("throws on non-404 errors", async () => {
      mockGet.mockRejectedValueOnce(new ApiError(500, "Internal Server Error"));

      await expect(getSessionTranscript("bd-123", "s1")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });

    it("URL-encodes both IDs", async () => {
      mockGet.mockResolvedValueOnce({ data: { entries: [] } });

      await getSessionTranscript("task id", "session id");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/tasks/task%20id/sessions/session%20id/transcript",
      );
    });
  });

  // ============= getSessionDiff =============

  describe("getSessionDiff", () => {
    it("returns diff text on success", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        text: async () => "diff --git a/file.ts b/file.ts\n+added line\n",
      });

      const result = await getSessionDiff("bd-123", "s1");

      expect(result).toBe("diff --git a/file.ts b/file.ts\n+added line\n");
      expect(mockFetch).toHaveBeenCalledWith(
        "/api/tasks/bd-123/sessions/s1/diff",
        { headers: {} },
      );
    });

    it("returns null on 404", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        statusText: "Not Found",
      });

      const result = await getSessionDiff("bd-123", "s-missing");

      expect(result).toBeNull();
    });

    it("throws ApiError on non-404 error", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
      });

      await expect(getSessionDiff("bd-123", "s1")).rejects.toThrow(ApiError);
    });

    it("includes Authorization header when auth token is available", async () => {
      mockGetAuthToken.mockReturnValue("test-token");
      mockFetch.mockResolvedValueOnce({
        ok: true,
        text: async () => "",
      });

      await getSessionDiff("bd-123", "s1");

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers).toEqual({
        Authorization: "Bearer test-token",
      });
    });

    it("omits Authorization header when auth token is null", async () => {
      mockGetAuthToken.mockReturnValue(null);
      mockFetch.mockResolvedValueOnce({
        ok: true,
        text: async () => "",
      });

      await getSessionDiff("bd-123", "s1");

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers).toEqual({});
    });

    it("URL-encodes both IDs in the fetch URL", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        text: async () => "",
      });

      await getSessionDiff("task/id", "session/id");

      const [url] = mockFetch.mock.calls[0];
      expect(url).toBe("/api/tasks/task%2Fid/sessions/session%2Fid/diff");
    });
  });
});
