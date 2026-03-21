/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, get, post, patch, del } from "./client";
import {
  listTerminalSessions,
  fetchTerminalToken,
  buildTerminalWsUrl,
  seedTerminalSession,
  listTabMetadata,
  getTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
  fetchScrollback,
  getTerminalState,
  patchTerminalState,
} from "./terminal";
import type {
  TerminalSessionInfo,
  TabMetadata,
  IssueContext,
} from "./terminal";

// Mock the client module
vi.mock("./client", () => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
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

const mockGet = get as ReturnType<typeof vi.fn>;
const mockPost = post as ReturnType<typeof vi.fn>;
const mockPatch = patch as ReturnType<typeof vi.fn>;
const mockDel = del as ReturnType<typeof vi.fn>;

describe("terminal API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ============= listTerminalSessions =============

  describe("listTerminalSessions", () => {
    it("returns sessions on successful response", async () => {
      const sessions: TerminalSessionInfo[] = [
        { name: "session-1", label: "Session 1", created: 1000 },
        { name: "session-2", label: "Session 2", created: 2000 },
      ];

      mockGet.mockResolvedValue({
        success: true,
        data: { sessions },
      });

      const result = await listTerminalSessions();

      expect(result).toEqual(sessions);
      expect(mockGet).toHaveBeenCalledWith("/api/terminal/sessions");
    });

    it("returns empty array when server returns no sessions", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { sessions: [] },
      });

      const result = await listTerminalSessions();

      expect(result).toEqual([]);
    });

    it("throws ApiError when response indicates failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "terminal sessions unavailable",
      });

      await expect(listTerminalSessions()).rejects.toThrow(ApiError);
      await expect(listTerminalSessions()).rejects.toThrow(
        "terminal sessions unavailable",
      );
    });

    it("propagates network errors from client", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(listTerminalSessions()).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= fetchTerminalToken =============

  describe("fetchTerminalToken", () => {
    it("returns token on successful response", async () => {
      mockGet.mockResolvedValue({ token: "abc123" });

      const token = await fetchTerminalToken("my-session");

      expect(token).toBe("abc123");
      expect(mockGet).toHaveBeenCalledWith(
        "/api/terminal/token?session=my-session",
      );
    });

    it("URL-encodes the session name", async () => {
      mockGet.mockResolvedValue({ token: "tok" });

      await fetchTerminalToken("session with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/terminal/token?session=session%20with%20spaces",
      );
    });

    it("returns null on network error", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      const token = await fetchTerminalToken("my-session");

      expect(token).toBeNull();
    });

    it("returns null on any thrown error", async () => {
      mockGet.mockRejectedValue(new Error("unexpected failure"));

      const token = await fetchTerminalToken("my-session");

      expect(token).toBeNull();
    });
  });

  // ============= listTabMetadata =============

  describe("listTabMetadata", () => {
    it("returns tab metadata on success", async () => {
      const tabs: TabMetadata[] = [
        {
          session_name: "session-1",
          label: "Session 1",
          notes: "",
          sort_order: 0,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      ];

      mockGet.mockResolvedValue({ success: true, data: tabs });

      const result = await listTabMetadata();
      expect(result).toEqual(tabs);
      expect(mockGet).toHaveBeenCalledWith("/api/terminal/tabs");
    });

    it("throws on failure response", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "no Redis",
      });

      await expect(listTabMetadata()).rejects.toThrow(ApiError);
    });

    it("returns empty array on 404 ApiError (tab metadata disabled)", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const result = await listTabMetadata();

      expect(result).toEqual([]);
    });

    it("returns empty array on 503 ApiError (Redis unavailable)", async () => {
      mockGet.mockRejectedValue(new ApiError(503, "Service Unavailable"));

      const result = await listTabMetadata();

      expect(result).toEqual([]);
    });

    it("re-throws non-404/503 ApiError", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(listTabMetadata()).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });

    it("re-throws non-ApiError errors", async () => {
      mockGet.mockRejectedValue(new Error("network failure"));

      await expect(listTabMetadata()).rejects.toThrow("network failure");
    });
  });

  // ============= getTabMetadata =============

  describe("getTabMetadata", () => {
    it("returns tab metadata for a session", async () => {
      const tab: TabMetadata = {
        session_name: "test",
        label: "Test",
        notes: "notes",
        sort_order: 1,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      };

      mockGet.mockResolvedValue({ success: true, data: tab });

      const result = await getTabMetadata("test");
      expect(result).toEqual(tab);
      expect(mockGet).toHaveBeenCalledWith("/api/terminal/tabs/test");
    });

    it("URL-encodes the session name", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          session_name: "my-tab",
          label: "My Tab",
          notes: "",
          sort_order: 0,
          created_at: "",
          updated_at: "",
        },
      });

      await getTabMetadata("my-tab");
      expect(mockGet).toHaveBeenCalledWith("/api/terminal/tabs/my-tab");
    });
  });

  // ============= patchTabMetadata =============

  describe("patchTabMetadata", () => {
    it("sends partial update", async () => {
      const updated: TabMetadata = {
        session_name: "test",
        label: "Updated",
        notes: "",
        sort_order: 1,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-02T00:00:00Z",
      };

      mockPatch.mockResolvedValue({ success: true, data: updated });

      const result = await patchTabMetadata("test", { label: "Updated" });
      expect(result).toEqual(updated);
      expect(mockPatch).toHaveBeenCalledWith("/api/terminal/tabs/test", {
        label: "Updated",
      });
    });
  });

  // ============= deleteTabMetadata =============

  describe("deleteTabMetadata", () => {
    it("sends delete request", async () => {
      mockDel.mockResolvedValue({ success: true });

      await deleteTabMetadata("test");
      expect(mockDel).toHaveBeenCalledWith("/api/terminal/tabs/test");
    });
  });

  // ============= seedTerminalSession =============

  describe("seedTerminalSession", () => {
    it("sends POST to correct URL with context body", async () => {
      mockPost.mockResolvedValue({ success: true });

      const context: IssueContext = {
        issue_id: "PROJ-123",
        title: "Fix login bug",
        description: "Users cannot log in",
        design: "Use OAuth2",
        blockers: [{ id: "PROJ-100", title: "Auth service down" }],
      };

      await seedTerminalSession("my-session", context);

      expect(mockPost).toHaveBeenCalledWith(
        "/api/terminal/sessions/my-session/seed",
        context,
      );
    });

    it("URL-encodes the session name", async () => {
      mockPost.mockResolvedValue({ success: true });

      await seedTerminalSession("session with spaces", {
        issue_id: "X-1",
        title: "Test",
      });

      expect(mockPost).toHaveBeenCalledWith(
        "/api/terminal/sessions/session%20with%20spaces/seed",
        { issue_id: "X-1", title: "Test" },
      );
    });

    it("sends minimal context without optional fields", async () => {
      mockPost.mockResolvedValue({ success: true });

      const context: IssueContext = {
        issue_id: "X-1",
        title: "Simple issue",
      };

      await seedTerminalSession("test-session", context);

      expect(mockPost).toHaveBeenCalledWith(
        "/api/terminal/sessions/test-session/seed",
        context,
      );
    });

    it("propagates errors from client", async () => {
      mockPost.mockRejectedValue(new ApiError(404, "Not Found"));

      await expect(
        seedTerminalSession("missing", { issue_id: "X-1", title: "Test" }),
      ).rejects.toThrow("API Error: 404 Not Found");
    });
  });

  // ============= buildTerminalWsUrl =============

  describe("buildTerminalWsUrl", () => {
    let originalLocation: Location;

    beforeEach(() => {
      originalLocation = window.location;
    });

    afterEach(() => {
      Object.defineProperty(window, "location", {
        value: originalLocation,
        writable: true,
      });
    });

    it("builds ws: URL for http: protocol", () => {
      Object.defineProperty(window, "location", {
        value: { protocol: "http:", host: "localhost:8080" },
        writable: true,
      });

      const url = buildTerminalWsUrl("my-session", "tok123");

      expect(url).toBe(
        "ws://localhost:8080/api/terminal/ws?session=my-session&token=tok123",
      );
    });

    it("builds wss: URL for https: protocol", () => {
      Object.defineProperty(window, "location", {
        value: { protocol: "https:", host: "example.com" },
        writable: true,
      });

      const url = buildTerminalWsUrl("my-session", "tok123");

      expect(url).toBe(
        "wss://example.com/api/terminal/ws?session=my-session&token=tok123",
      );
    });

    it("omits token parameter when token is null", () => {
      Object.defineProperty(window, "location", {
        value: { protocol: "http:", host: "localhost:3000" },
        writable: true,
      });

      const url = buildTerminalWsUrl("my-session", null);

      expect(url).toBe(
        "ws://localhost:3000/api/terminal/ws?session=my-session",
      );
      expect(url).not.toContain("token=");
    });

    it("URL-encodes session name and token", () => {
      Object.defineProperty(window, "location", {
        value: { protocol: "http:", host: "localhost:8080" },
        writable: true,
      });

      const url = buildTerminalWsUrl("session with spaces", "tok&en=val");

      expect(url).toBe(
        "ws://localhost:8080/api/terminal/ws?session=session%20with%20spaces&token=tok%26en%3Dval",
      );
    });
  });

  // ============= fetchScrollback =============

  describe("fetchScrollback", () => {
    it("returns scrollback content and line count on success", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { content: "line1\nline2\nline3", lines: 3 },
      });

      const result = await fetchScrollback("my-session");

      expect(result).toEqual({ content: "line1\nline2\nline3", lines: 3 });
      expect(mockGet).toHaveBeenCalledWith(
        "/api/terminal/sessions/my-session/scrollback",
      );
    });

    it("URL-encodes the session name", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { content: "", lines: 0 },
      });

      await fetchScrollback("session with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/terminal/sessions/session%20with%20spaces/scrollback",
      );
    });

    it("throws ApiError when response indicates failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "session not found",
      });

      await expect(fetchScrollback("missing")).rejects.toThrow(ApiError);
      await expect(fetchScrollback("missing")).rejects.toThrow(
        "session not found",
      );
    });

    it("propagates network errors from client", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(fetchScrollback("my-session")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= getTerminalState =============

  describe("getTerminalState", () => {
    it("returns terminal state with active_tab", async () => {
      mockGet.mockResolvedValue({ active_tab: "session-abc" });

      const result = await getTerminalState();

      expect(result).toEqual({ active_tab: "session-abc" });
      expect(mockGet).toHaveBeenCalledWith("/api/terminal/state");
    });

    it("returns empty active_tab when no state set", async () => {
      mockGet.mockResolvedValue({ active_tab: "" });

      const result = await getTerminalState();

      expect(result).toEqual({ active_tab: "" });
    });

    it("propagates network errors from client", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(getTerminalState()).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= patchTerminalState =============

  describe("patchTerminalState", () => {
    it("sends PATCH with active_tab", async () => {
      mockPatch.mockResolvedValue({ active_tab: "session-xyz" });

      await patchTerminalState({ active_tab: "session-xyz" });

      expect(mockPatch).toHaveBeenCalledWith("/api/terminal/state", {
        active_tab: "session-xyz",
      });
    });

    it("sends PATCH with empty active_tab to clear state", async () => {
      mockPatch.mockResolvedValue({ active_tab: "" });

      await patchTerminalState({ active_tab: "" });

      expect(mockPatch).toHaveBeenCalledWith("/api/terminal/state", {
        active_tab: "",
      });
    });

    it("propagates network errors from client", async () => {
      mockPatch.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(patchTerminalState({ active_tab: "test" })).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });
});
