/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, get, put, del } from "../client";
import {
  fetchIssueTabState,
  saveIssueTabState,
  deleteIssueTabState,
} from "../issueTabs";
import type { IssueTabState, IssueTab } from "../issueTabs";

// Mock the client module
vi.mock("../client", () => ({
  get: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
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
const mockPut = put as ReturnType<typeof vi.fn>;
const mockDel = del as ReturnType<typeof vi.fn>;

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

      mockGet.mockResolvedValue({
        success: true,
        data: state,
      });

      const result = await fetchIssueTabState("test-ws-id", "PROJ-123");

      expect(result).toEqual(state);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-123/tabs",
      );
    });

    it("returns null on empty/null data", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: null,
      });

      const result = await fetchIssueTabState("test-ws-id", "PROJ-456");

      expect(result).toBeNull();
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-456/tabs",
      );
    });

    it("throws ApiError when response indicates failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "issue tab state not available (no Redis)",
      });

      await expect(
        fetchIssueTabState("test-ws-id", "PROJ-789"),
      ).rejects.toThrow(ApiError);
      await expect(
        fetchIssueTabState("test-ws-id", "PROJ-789"),
      ).rejects.toThrow("issue tab state not available (no Redis)");
    });

    it("URL-encodes the issue ID", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: null,
      });

      await fetchIssueTabState("test-ws-id", "issue with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/issue%20with%20spaces/tabs",
      );
    });

    it("propagates network errors from client", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      await expect(
        fetchIssueTabState("test-ws-id", "PROJ-ERR"),
      ).rejects.toThrow("API Error: 500 Internal Server Error");
    });
  });

  // ============= saveIssueTabState =============

  describe("saveIssueTabState", () => {
    it("sends correct PUT body", async () => {
      mockPut.mockResolvedValue({
        success: true,
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
      });

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
      ];

      await saveIssueTabState("test-ws-id", "PROJ-100", tabs, "details");

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-100/tabs",
        {
          tabs,
          active_tab_id: "details",
        },
      );
    });

    it("sends multiple tabs with terminal session data", async () => {
      mockPut.mockResolvedValue({
        success: true,
        data: {},
      });

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

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-200/tabs",
        {
          tabs,
          active_tab_id: "terminal-s1",
        },
      );
    });

    it("sends terminal tab with backend field", async () => {
      mockPut.mockResolvedValue({
        success: true,
        data: {},
      });

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

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-BACKEND/tabs",
        {
          tabs,
          active_tab_id: "terminal-s1",
        },
      );
      // Verify backend is present in the sent tab data
      const sentTabs = mockPut.mock.calls[0][1].tabs;
      expect(sentTabs[1].backend).toBe("claude");
    });

    it("sends terminal tab without backend for legacy format", async () => {
      mockPut.mockResolvedValue({
        success: true,
        data: {},
      });

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

      const sentTabs = mockPut.mock.calls[0][1].tabs;
      expect(sentTabs[1].backend).toBeUndefined();
      expect(sentTabs[1].session_name).toBe("lead-claude-1");
    });

    it("URL-encodes the issue ID", async () => {
      mockPut.mockResolvedValue({ success: true, data: {} });

      await saveIssueTabState(
        "test-ws-id",
        "issue/with/slashes",
        [],
        "details",
      );

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/issue%2Fwith%2Fslashes/tabs",
        { tabs: [], active_tab_id: "details" },
      );
    });
  });

  // ============= deleteIssueTabState =============

  describe("deleteIssueTabState", () => {
    it("calls correct endpoint", async () => {
      mockDel.mockResolvedValue({ success: true });

      await deleteIssueTabState("test-ws-id", "PROJ-300");

      expect(mockDel).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/PROJ-300/tabs",
      );
    });

    it("URL-encodes the issue ID", async () => {
      mockDel.mockResolvedValue({ success: true });

      await deleteIssueTabState("test-ws-id", "issue with spaces");

      expect(mockDel).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/issues/issue%20with%20spaces/tabs",
      );
    });

    it("propagates errors from client", async () => {
      mockDel.mockRejectedValue(new ApiError(503, "Service Unavailable"));

      await expect(
        deleteIssueTabState("test-ws-id", "PROJ-ERR"),
      ).rejects.toThrow("API Error: 503 Service Unavailable");
    });
  });
});
