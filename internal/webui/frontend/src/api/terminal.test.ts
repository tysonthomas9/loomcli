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

// Mock the client module — export both legacy helpers AND the typed `api` object
vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return {
    ...actual,
    // Legacy fetch wrappers (still used by fetchTerminalToken, restartTerminalSession, etc.)
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    // openapi-fetch typed client
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

// Import the mocked api after vi.mock so we can reference it in tests
import { api } from "./client";

const mockGet = get as ReturnType<typeof vi.fn>;
const _mockPost = post as ReturnType<typeof vi.fn>;
const _mockPatch = patch as ReturnType<typeof vi.fn>;
const _mockDel = del as ReturnType<typeof vi.fn>;

const mockApiGET = api.GET as ReturnType<typeof vi.fn>;
const mockApiPOST = api.POST as ReturnType<typeof vi.fn>;
const mockApiPATCH = api.PATCH as ReturnType<typeof vi.fn>;
const mockApiDELETE = api.DELETE as ReturnType<typeof vi.fn>;

// Helper: build a successful openapi-fetch response
function okResponse<T>(data: T) {
  return { data, error: undefined, response: new Response() };
}

// Helper: build an error openapi-fetch response
function errResponse(status: number, statusText: string, body?: unknown) {
  return {
    data: undefined,
    error: body ?? statusText,
    response: new Response(null, { status, statusText }),
  };
}

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

      mockApiGET.mockResolvedValue(
        okResponse({ success: true, data: { sessions } }),
      );

      const result = await listTerminalSessions("test-ws-id");

      expect(result).toEqual(sessions);
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions",
        {
          params: { path: { ws: "test-ws-id" } },
        },
      );
    });

    it("returns empty array when server returns no sessions", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({ success: true, data: { sessions: [] } }),
      );

      const result = await listTerminalSessions("test-ws-id");

      expect(result).toEqual([]);
    });

    it("throws ApiError when response indicates failure", async () => {
      mockApiGET.mockResolvedValue(
        errResponse(400, "Bad Request", "terminal sessions unavailable"),
      );

      await expect(listTerminalSessions("test-ws-id")).rejects.toThrow(
        ApiError,
      );
      await expect(listTerminalSessions("test-ws-id")).rejects.toSatisfy(
        (err: ApiError) => err.body === "terminal sessions unavailable",
      );
    });

    it("propagates network errors from client", async () => {
      mockApiGET.mockResolvedValue(errResponse(500, "Internal Server Error"));

      await expect(listTerminalSessions("test-ws-id")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= fetchTerminalToken =============

  describe("fetchTerminalToken", () => {
    it("returns token on successful response", async () => {
      mockGet.mockResolvedValue({ token: "abc123" });

      const token = await fetchTerminalToken("test-ws-id", "my-session");

      expect(token).toBe("abc123");
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/terminal/token?session=my-session",
      );
    });

    it("URL-encodes the session name", async () => {
      mockGet.mockResolvedValue({ token: "tok" });

      await fetchTerminalToken("test-ws-id", "session with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/terminal/token?session=session%20with%20spaces",
      );
    });

    it("returns null on network error", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      const token = await fetchTerminalToken("test-ws-id", "my-session");

      expect(token).toBeNull();
    });

    it("returns null on any thrown error", async () => {
      mockGet.mockRejectedValue(new Error("unexpected failure"));

      const token = await fetchTerminalToken("test-ws-id", "my-session");

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
          pinned: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      ];

      mockApiGET.mockResolvedValue(okResponse({ success: true, data: tabs }));

      const result = await listTabMetadata("ws-123");
      expect(result).toEqual(tabs);
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/tabs",
        {
          params: { path: { ws: "ws-123" } },
        },
      );
    });

    it("throws on failure response", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({ success: false, error: "no Redis" }),
      );

      await expect(listTabMetadata("ws-123")).rejects.toThrow(ApiError);
    });

    it("returns empty array on 404 ApiError (tab metadata disabled)", async () => {
      mockApiGET.mockResolvedValue(errResponse(404, "Not Found"));

      const result = await listTabMetadata("ws-123");

      expect(result).toEqual([]);
    });

    it("returns empty array on 503 ApiError (Redis unavailable)", async () => {
      mockApiGET.mockResolvedValue(errResponse(503, "Service Unavailable"));

      const result = await listTabMetadata("ws-123");

      expect(result).toEqual([]);
    });

    it("re-throws non-404/503 ApiError", async () => {
      mockApiGET.mockResolvedValue(errResponse(500, "Internal Server Error"));

      await expect(listTabMetadata("ws-123")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });

    it("re-throws non-ApiError errors", async () => {
      mockApiGET.mockRejectedValue(new Error("network failure"));

      await expect(listTabMetadata("ws-123")).rejects.toThrow(
        "network failure",
      );
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
        pinned: false,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      };

      mockApiGET.mockResolvedValue(okResponse({ success: true, data: tab }));

      const result = await getTabMetadata("ws-123", "test");
      expect(result).toEqual(tab);
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/tabs/{session}",
        {
          params: { path: { ws: "ws-123", session: "test" } },
        },
      );
    });

    it("URL-encodes the session name", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({
          success: true,
          data: {
            session_name: "my-tab",
            label: "My Tab",
            notes: "",
            sort_order: 0,
            pinned: false,
            created_at: "",
            updated_at: "",
          },
        }),
      );

      await getTabMetadata("ws-123", "my-tab");
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/tabs/{session}",
        {
          params: { path: { ws: "ws-123", session: "my-tab" } },
        },
      );
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
        pinned: false,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-02T00:00:00Z",
      };

      // patchTabMetadata does PATCH then refetches via getTabMetadata (GET)
      mockApiPATCH.mockResolvedValue(okResponse({ success: true }));
      mockApiGET.mockResolvedValue(
        okResponse({ success: true, data: updated }),
      );

      const result = await patchTabMetadata("ws-123", "test", {
        label: "Updated",
      });
      expect(result).toEqual(updated);
      expect(mockApiPATCH).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/tabs/{session}",
        {
          params: { path: { ws: "ws-123", session: "test" } },
          body: { label: "Updated" },
        },
      );
    });
  });

  // ============= deleteTabMetadata =============

  describe("deleteTabMetadata", () => {
    it("sends delete request", async () => {
      mockApiDELETE.mockResolvedValue(okResponse({ success: true }));

      await deleteTabMetadata("ws-123", "test");
      expect(mockApiDELETE).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/tabs/{session}",
        {
          params: { path: { ws: "ws-123", session: "test" } },
        },
      );
    });
  });

  // ============= seedTerminalSession =============

  describe("seedTerminalSession", () => {
    it("sends POST to correct URL with context body", async () => {
      mockApiPOST.mockResolvedValue(okResponse({ success: true }));

      const context: IssueContext = {
        issue_id: "PROJ-123",
        title: "Fix login bug",
        description: "Users cannot log in",
        design: "Use OAuth2",
        blockers: [{ id: "PROJ-100", title: "Auth service down" }],
      };

      await seedTerminalSession("test-ws-id", "my-session", context);

      expect(mockApiPOST).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions/{name}/seed",
        {
          params: { path: { ws: "test-ws-id", name: "my-session" } },
          body: context,
        },
      );
    });

    it("URL-encodes the session name", async () => {
      mockApiPOST.mockResolvedValue(okResponse({ success: true }));

      await seedTerminalSession("test-ws-id", "session with spaces", {
        issue_id: "X-1",
        title: "Test",
      });

      expect(mockApiPOST).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions/{name}/seed",
        {
          params: {
            path: { ws: "test-ws-id", name: "session with spaces" },
          },
          body: { issue_id: "X-1", title: "Test" },
        },
      );
    });

    it("sends minimal context without optional fields", async () => {
      mockApiPOST.mockResolvedValue(okResponse({ success: true }));

      const context: IssueContext = {
        issue_id: "X-1",
        title: "Simple issue",
      };

      await seedTerminalSession("test-ws-id", "test-session", context);

      expect(mockApiPOST).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions/{name}/seed",
        {
          params: { path: { ws: "test-ws-id", name: "test-session" } },
          body: context,
        },
      );
    });

    it("propagates errors from client", async () => {
      mockApiPOST.mockResolvedValue(errResponse(404, "Not Found"));

      await expect(
        seedTerminalSession("test-ws-id", "missing", {
          issue_id: "X-1",
          title: "Test",
        }),
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
        value: { origin: "http://localhost:8080" },
        writable: true,
      });

      const url = buildTerminalWsUrl("test-ws-id", "my-session", "tok123");

      expect(url).toBe(
        "ws://localhost:8080/api/workspaces/test-ws-id/terminal/ws?session=my-session&token=tok123",
      );
    });

    it("builds wss: URL for https: protocol", () => {
      Object.defineProperty(window, "location", {
        value: { origin: "https://example.com" },
        writable: true,
      });

      const url = buildTerminalWsUrl("test-ws-id", "my-session", "tok123");

      expect(url).toBe(
        "wss://example.com/api/workspaces/test-ws-id/terminal/ws?session=my-session&token=tok123",
      );
    });

    it("omits token parameter when token is null", () => {
      Object.defineProperty(window, "location", {
        value: { origin: "http://localhost:3000" },
        writable: true,
      });

      const url = buildTerminalWsUrl("test-ws-id", "my-session", null);

      expect(url).toBe(
        "ws://localhost:3000/api/workspaces/test-ws-id/terminal/ws?session=my-session",
      );
      expect(url).not.toContain("token=");
    });

    it("URL-encodes session name, token, and workspace", () => {
      Object.defineProperty(window, "location", {
        value: { origin: "http://localhost:8080" },
        writable: true,
      });

      const url = buildTerminalWsUrl(
        "test-ws-id",
        "session with spaces",
        "tok&en=val",
      );

      expect(url).toBe(
        "ws://localhost:8080/api/workspaces/test-ws-id/terminal/ws?session=session%20with%20spaces&token=tok%26en%3Dval",
      );
    });

    it("includes empty workspace in URL path when workspaceId is empty", () => {
      Object.defineProperty(window, "location", {
        value: { origin: "http://localhost:8080" },
        writable: true,
      });

      const url = buildTerminalWsUrl("", "my-session", "tok123");

      expect(url).toBe(
        "ws://localhost:8080/api/workspaces//terminal/ws?session=my-session&token=tok123",
      );
    });
  });

  // ============= fetchScrollback =============

  describe("fetchScrollback", () => {
    it("returns scrollback content and line count on success", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({
          success: true,
          data: { content: "line1\nline2\nline3", lines: 3 },
        }),
      );

      const result = await fetchScrollback("test-ws-id", "my-session");

      expect(result).toEqual({ content: "line1\nline2\nline3", lines: 3 });
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions/{session}/scrollback",
        {
          params: {
            path: { ws: "test-ws-id", session: "my-session" },
          },
        },
      );
    });

    it("URL-encodes the session name", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({ success: true, data: { content: "", lines: 0 } }),
      );

      await fetchScrollback("test-ws-id", "session with spaces");

      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/sessions/{session}/scrollback",
        {
          params: {
            path: {
              ws: "test-ws-id",
              session: "session with spaces",
            },
          },
        },
      );
    });

    it("throws ApiError when response indicates failure", async () => {
      mockApiGET.mockResolvedValue(
        okResponse({ success: false, error: "session not found" }),
      );

      await expect(fetchScrollback("test-ws-id", "missing")).rejects.toThrow(
        ApiError,
      );
      await expect(fetchScrollback("test-ws-id", "missing")).rejects.toThrow(
        "session not found",
      );
    });

    it("propagates network errors from client", async () => {
      mockApiGET.mockResolvedValue(errResponse(500, "Internal Server Error"));

      await expect(fetchScrollback("test-ws-id", "my-session")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= getTerminalState =============

  describe("getTerminalState", () => {
    it("returns terminal state with active_tab", async () => {
      mockApiGET.mockResolvedValue(okResponse({ active_tab: "session-abc" }));

      const result = await getTerminalState("test-ws-id");

      expect(result).toEqual({ active_tab: "session-abc" });
      expect(mockApiGET).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/state",
        {
          params: { path: { ws: "test-ws-id" } },
        },
      );
    });

    it("returns empty active_tab when no state set", async () => {
      mockApiGET.mockResolvedValue(okResponse({ active_tab: "" }));

      const result = await getTerminalState("test-ws-id");

      expect(result).toEqual({ active_tab: "" });
    });

    it("propagates network errors from client", async () => {
      mockApiGET.mockResolvedValue(errResponse(500, "Internal Server Error"));

      await expect(getTerminalState("test-ws-id")).rejects.toThrow(
        "API Error: 500 Internal Server Error",
      );
    });
  });

  // ============= patchTerminalState =============

  describe("patchTerminalState", () => {
    it("sends PATCH with active_tab", async () => {
      mockApiPATCH.mockResolvedValue(okResponse({ active_tab: "session-xyz" }));

      await patchTerminalState("test-ws-id", { active_tab: "session-xyz" });

      expect(mockApiPATCH).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/state",
        {
          params: { path: { ws: "test-ws-id" } },
          body: { active_tab: "session-xyz" },
        },
      );
    });

    it("sends PATCH with empty active_tab to clear state", async () => {
      mockApiPATCH.mockResolvedValue(okResponse({ active_tab: "" }));

      await patchTerminalState("test-ws-id", { active_tab: "" });

      expect(mockApiPATCH).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/terminal/state",
        {
          params: { path: { ws: "test-ws-id" } },
          body: { active_tab: "" },
        },
      );
    });

    it("propagates network errors from client", async () => {
      mockApiPATCH.mockResolvedValue(errResponse(500, "Internal Server Error"));

      await expect(
        patchTerminalState("test-ws-id", { active_tab: "test" }),
      ).rejects.toThrow("API Error: 500 Internal Server Error");
    });
  });
});
