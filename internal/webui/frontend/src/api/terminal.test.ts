/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError, get, patch, del } from "./client";
import {
  listTerminalSessions,
  fetchTerminalToken,
  buildTerminalWsUrl,
  listTabMetadata,
  getTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "./terminal";
import type { TerminalSessionInfo, TabMetadata } from "./terminal";

// Mock the client module
vi.mock("./client", () => ({
  get: vi.fn(),
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
});
