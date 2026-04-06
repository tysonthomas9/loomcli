/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, api } from "../client";
import {
  fetchIssueTabState,
  saveIssueTabState,
  deleteIssueTabState,
} from "../issueTabs";
import type { IssueTabState, IssueTab } from "../issueTabs";

// Mock the client module
vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
  return {
    ...actual,
    get: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
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
const mockApiPut = vi.mocked(api.PUT);
const mockApiDelete = vi.mocked(api.DELETE);

describe("issueTabs API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ============= fetchIssueTabState =============

  describe("fetchIssueTabState", () => {
    it("parses response correctly", async () => {
      const state: IssueTabState = {
        issue_id: "PROJ-123",
        tabs: [
          { id: "details", type: "details", label: "Details", sort_order: 0 },
          {
            id: "terminal-s1",
            type: "terminal",
            label: "Terminal 1",
            session_name: "s1",
            sort_order: 1,
          },
        ],
        active_tab_id: "details",
        updated_at: "2026-03-15T10:00:00Z",
      };

      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: state,
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await fetchIssueTabState("test-ws-id", "PROJ-123");

      expect(result).toEqual(state);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-123" },
          },
        },
      );
    });

    it("returns null on empty/null data", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: null,
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await fetchIssueTabState("test-ws-id", "PROJ-456");

      expect(result).toBeNull();
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-456" },
          },
        },
      );
    });

    it("throws ApiError when response indicates failure", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "issue tab state not available (no Redis)" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        fetchIssueTabState("test-ws-id", "PROJ-789"),
      ).rejects.toThrow(ApiError);
    });

    it("passes workspace and issue IDs as path params", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          success: true,
          data: null,
        },
        error: undefined,
        response: new Response(),
      } as never);

      await fetchIssueTabState("test-ws-id", "issue with spaces");

      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "issue with spaces" },
          },
        },
      );
    });

    it("propagates network errors from client", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        fetchIssueTabState("test-ws-id", "PROJ-ERR"),
      ).rejects.toThrow(ApiError);
    });
  });

  // ============= saveIssueTabState =============

  describe("saveIssueTabState", () => {
    it("sends correct PUT body", async () => {
      mockApiPut.mockResolvedValueOnce({
        data: {
          issue_id: "PROJ-100",
          tabs: [
            {
              id: "details",
              type: "details",
              label: "Details",
              sort_order: 0,
            },
          ],
          active_tab_id: "details",
          updated_at: "2026-03-15T10:00:00Z",
        },
        error: undefined,
        response: new Response(),
      } as never);

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
      ];

      await saveIssueTabState("test-ws-id", "PROJ-100", tabs, "details");

      expect(mockApiPut).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-100" },
          },
          body: {
            tabs: [
              {
                id: "details",
                type: "details",
                label: "Details",
                sort_order: 0,
              },
            ],
            active_tab_id: "details",
          },
        },
      );
    });

    it("sends multiple tabs with terminal session data", async () => {
      mockApiPut.mockResolvedValueOnce({
        data: {},
        error: undefined,
        response: new Response(),
      } as never);

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
        { id: "logs", type: "logs", label: "Logs", sort_order: 1 },
        {
          id: "terminal-s1",
          type: "terminal",
          label: "Terminal 1",
          session_name: "s1",
          sort_order: 2,
        },
      ];

      await saveIssueTabState("test-ws-id", "PROJ-200", tabs, "terminal-s1");

      expect(mockApiPut).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-200" },
          },
          body: {
            tabs: [
              {
                id: "details",
                type: "details",
                label: "Details",
                sort_order: 0,
              },
              {
                id: "logs",
                type: "logs",
                label: "Logs",
                sort_order: 1,
              },
              {
                id: "terminal-s1",
                type: "terminal",
                label: "Terminal 1",
                session_name: "s1",
                sort_order: 2,
              },
            ],
            active_tab_id: "terminal-s1",
          },
        },
      );
    });

    it("sends terminal tab with backend field", async () => {
      mockApiPut.mockResolvedValueOnce({
        data: {},
        error: undefined,
        response: new Response(),
      } as never);

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
        {
          id: "terminal-s1",
          type: "terminal",
          label: "Terminal 1",
          session_name: "lead-claude-1",
          backend: "claude",
          sort_order: 1,
        },
      ];

      await saveIssueTabState(
        "test-ws-id",
        "PROJ-BACKEND",
        tabs,
        "terminal-s1",
      );

      expect(mockApiPut).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-BACKEND" },
          },
          body: {
            tabs: expect.arrayContaining([
              expect.objectContaining({
                id: "terminal-s1",
                backend: "claude",
              }),
            ]),
            active_tab_id: "terminal-s1",
          },
        },
      );
      // Verify backend is present in the sent tab data
      const sentBody = mockApiPut.mock.calls[0][1]!;
      const sentTabs = (sentBody as { body: { tabs: IssueTab[] } }).body.tabs;
      expect(sentTabs[1].backend).toBe("claude");
    });

    it("sends terminal tab without backend for legacy format", async () => {
      mockApiPut.mockResolvedValueOnce({
        data: {},
        error: undefined,
        response: new Response(),
      } as never);

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
        {
          id: "terminal-s1",
          type: "terminal",
          label: "Terminal 1",
          session_name: "lead-claude-1",
          sort_order: 1,
        },
      ];

      await saveIssueTabState("test-ws-id", "PROJ-LEGACY", tabs, "terminal-s1");

      const sentBody = mockApiPut.mock.calls[0][1]!;
      const sentTabs = (sentBody as { body: { tabs: IssueTab[] } }).body.tabs;
      expect(sentTabs[1].backend).toBeUndefined();
      expect(sentTabs[1].session_name).toBe("lead-claude-1");
    });

    it("passes workspace and issue IDs as path params", async () => {
      mockApiPut.mockResolvedValueOnce({
        data: {},
        error: undefined,
        response: new Response(),
      } as never);

      await saveIssueTabState(
        "test-ws-id",
        "issue/with/slashes",
        [],
        "details",
      );

      expect(mockApiPut).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "issue/with/slashes" },
          },
          body: { tabs: [], active_tab_id: "details" },
        },
      );
    });
  });

  // ============= deleteIssueTabState =============

  describe("deleteIssueTabState", () => {
    it("calls correct endpoint", async () => {
      mockApiDelete.mockResolvedValueOnce({
        data: { success: true },
        error: undefined,
        response: new Response(),
      } as never);

      await deleteIssueTabState("test-ws-id", "PROJ-300");

      expect(mockApiDelete).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "PROJ-300" },
          },
        },
      );
    });

    it("passes workspace and issue IDs as path params", async () => {
      mockApiDelete.mockResolvedValueOnce({
        data: { success: true },
        error: undefined,
        response: new Response(),
      } as never);

      await deleteIssueTabState("test-ws-id", "issue with spaces");

      expect(mockApiDelete).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/issues/{issueId}/tabs",
        {
          params: {
            path: { ws: "test-ws-id", issueId: "issue with spaces" },
          },
        },
      );
    });

    it("propagates errors from client", async () => {
      mockApiDelete.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Service Unavailable" },
        response: new Response(null, {
          status: 503,
          statusText: "Service Unavailable",
        }),
      } as never);

      await expect(
        deleteIssueTabState("test-ws-id", "PROJ-ERR"),
      ).rejects.toThrow(ApiError);
    });
  });
});
