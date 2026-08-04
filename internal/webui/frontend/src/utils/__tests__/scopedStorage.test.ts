/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for scopedStorage utility.
 */

import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";

import {
  wsKey,
  wsGet,
  wsSet,
  wsRemove,
  getLastWorkspaceId,
  setLastWorkspaceId,
} from "../scopedStorage";

describe("scopedStorage", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("wsKey", () => {
    it("builds a workspace-scoped key", () => {
      expect(wsKey("abc-123", "tree-collapsed")).toBe(
        "loom:abc-123:tree-collapsed",
      );
    });

    it("handles UUID-style workspace ID", () => {
      expect(
        wsKey("550e8400-e29b-41d4-a716-446655440000", "selected-repos"),
      ).toBe("loom:550e8400-e29b-41d4-a716-446655440000:selected-repos");
    });

    it("handles empty key suffix", () => {
      expect(wsKey("abc", "")).toBe("loom:abc:");
    });
  });

  describe("wsGet", () => {
    it("returns null when key missing", () => {
      expect(wsGet("abc", "tree-collapsed")).toBeNull();
    });

    it("returns stored value", () => {
      localStorage.setItem("loom:abc:tree-collapsed", "true");
      expect(wsGet("abc", "tree-collapsed")).toBe("true");
    });

    it("returns null when localStorage throws", () => {
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });
      expect(wsGet("abc", "tree-collapsed")).toBeNull();
    });
  });

  describe("wsSet", () => {
    it("persists value retrievable by wsGet", () => {
      wsSet("abc", "tree-collapsed", "true");
      expect(wsGet("abc", "tree-collapsed")).toBe("true");
    });

    it("silently ignores QuotaExceededError", () => {
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      });

      // Should not throw
      expect(() => wsSet("abc", "tree-collapsed", "true")).not.toThrow();
    });

    it("silently ignores SecurityError", () => {
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });

      expect(() => wsSet("abc", "tree-collapsed", "true")).not.toThrow();
    });
  });

  describe("wsRemove", () => {
    it("removes persisted value", () => {
      wsSet("abc", "tree-collapsed", "true");
      expect(wsGet("abc", "tree-collapsed")).toBe("true");

      wsRemove("abc", "tree-collapsed");
      expect(wsGet("abc", "tree-collapsed")).toBeNull();
    });

    it("silently ignores errors", () => {
      vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });

      expect(() => wsRemove("abc", "tree-collapsed")).not.toThrow();
    });
  });

  describe("getLastWorkspaceId", () => {
    it("returns null when no workspace ID stored", () => {
      expect(getLastWorkspaceId()).toBeNull();
    });

    it("returns stored workspace ID", () => {
      localStorage.setItem("loom:last-workspace-id", "ws-uuid-123");
      expect(getLastWorkspaceId()).toBe("ws-uuid-123");
    });

    it("returns null when localStorage throws", () => {
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });
      expect(getLastWorkspaceId()).toBeNull();
    });
  });

  describe("setLastWorkspaceId", () => {
    it("stores workspace ID retrievable by getLastWorkspaceId", () => {
      setLastWorkspaceId("ws-uuid-456");
      expect(getLastWorkspaceId()).toBe("ws-uuid-456");
    });

    it("silently ignores errors", () => {
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });
      expect(() => setLastWorkspaceId("ws-uuid-456")).not.toThrow();
    });
  });

  describe("workspace isolation", () => {
    it("different workspace IDs produce independent keys", () => {
      wsSet("ws-1", "tree-collapsed", "true");
      wsSet("ws-2", "tree-collapsed", "false");

      expect(wsGet("ws-1", "tree-collapsed")).toBe("true");
      expect(wsGet("ws-2", "tree-collapsed")).toBe("false");
    });

    it("removing from one workspace does not affect another", () => {
      wsSet("ws-1", "foo", "bar");
      wsSet("ws-2", "foo", "baz");

      wsRemove("ws-1", "foo");

      expect(wsGet("ws-1", "foo")).toBeNull();
      expect(wsGet("ws-2", "foo")).toBe("baz");
    });
  });
});
