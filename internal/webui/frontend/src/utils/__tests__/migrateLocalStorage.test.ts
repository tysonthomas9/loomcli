/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for migrateLocalStorage utility.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  migrateLocalStorage,
  getStorageVersion,
  CURRENT_VERSION,
  VERSION_KEY,
  V5_TO_V6_KEY_MAP,
  V6_TO_V7_SCOPED_KEYS,
} from "../migrateLocalStorage";

describe("migrateLocalStorage", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("fresh install", () => {
    it("stamps current version when localStorage is empty", () => {
      migrateLocalStorage();

      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });

    it("does not create V6 keys (hooks will use defaults)", () => {
      migrateLocalStorage();

      expect(localStorage.getItem("cortex:theme")).toBeNull();
      expect(localStorage.getItem("cortex:recent-assignees")).toBeNull();
      expect(localStorage.getItem("cortex:terminal-font-family")).toBeNull();
      expect(localStorage.getItem("cortex:terminal-font-size")).toBeNull();
    });
  });

  describe("full V5 migration", () => {
    it("migrates all V5 keys to V6 and removes V5 keys", () => {
      localStorage.setItem("theme-preference", "dark");
      localStorage.setItem(
        "beads-recent-assignees",
        JSON.stringify(["Alice", "Bob"]),
      );
      localStorage.setItem("terminal-font-family", '"Fira Code", monospace');
      localStorage.setItem("terminal-font-size", "16");

      migrateLocalStorage();

      // V6 keys exist with correct values
      expect(localStorage.getItem("cortex:theme")).toBe("dark");
      expect(localStorage.getItem("cortex:recent-assignees")).toBe(
        JSON.stringify(["Alice", "Bob"]),
      );
      expect(localStorage.getItem("cortex:terminal-font-family")).toBe(
        '"Fira Code", monospace',
      );
      expect(localStorage.getItem("cortex:terminal-font-size")).toBe("16");

      // V5 keys removed
      expect(localStorage.getItem("theme-preference")).toBeNull();
      expect(localStorage.getItem("beads-recent-assignees")).toBeNull();
      expect(localStorage.getItem("terminal-font-family")).toBeNull();
      expect(localStorage.getItem("terminal-font-size")).toBeNull();

      // Version stamped
      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });
  });

  describe("partial V5 data", () => {
    it("migrates only existing V5 keys", () => {
      localStorage.setItem("theme-preference", "dark");
      // No other V5 keys set

      migrateLocalStorage();

      expect(localStorage.getItem("cortex:theme")).toBe("dark");
      expect(localStorage.getItem("cortex:recent-assignees")).toBeNull();
      expect(localStorage.getItem("cortex:terminal-font-family")).toBeNull();
      expect(localStorage.getItem("cortex:terminal-font-size")).toBeNull();
      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });
  });

  describe("idempotency", () => {
    it("no-ops when version is already current", () => {
      localStorage.setItem(VERSION_KEY, CURRENT_VERSION);
      localStorage.setItem("cortex:theme", "light");

      const getItemSpy = vi.spyOn(Storage.prototype, "getItem");
      const setItemSpy = vi.spyOn(Storage.prototype, "setItem");

      migrateLocalStorage();

      // Only reads version key to check
      expect(getItemSpy).toHaveBeenCalledWith(VERSION_KEY);
      // No writes should happen
      expect(setItemSpy).not.toHaveBeenCalled();

      // Data unchanged
      expect(localStorage.getItem("cortex:theme")).toBe("light");
    });

    it("calling twice produces same result as once", () => {
      localStorage.setItem("theme-preference", "dark");
      localStorage.setItem("beads-recent-assignees", JSON.stringify(["Alice"]));

      migrateLocalStorage();
      migrateLocalStorage();

      expect(localStorage.getItem("cortex:theme")).toBe("dark");
      expect(localStorage.getItem("cortex:recent-assignees")).toBe(
        JSON.stringify(["Alice"]),
      );
      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });
  });

  describe("corrupted JSON", () => {
    it("removes corrupted beads-recent-assignees and does not create V6 key", () => {
      localStorage.setItem("beads-recent-assignees", "not json {{{");

      migrateLocalStorage();

      expect(localStorage.getItem("beads-recent-assignees")).toBeNull();
      expect(localStorage.getItem("cortex:recent-assignees")).toBeNull();
      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });
  });

  describe("partial prior migration recovery", () => {
    it("does not overwrite V6 key if it already exists", () => {
      // Simulate partial migration: V5 key still exists AND V6 key already written
      localStorage.setItem("theme-preference", "dark");
      localStorage.setItem("cortex:theme", "light");

      migrateLocalStorage();

      // V6 key preserved (not overwritten)
      expect(localStorage.getItem("cortex:theme")).toBe("light");
      // V5 key removed
      expect(localStorage.getItem("theme-preference")).toBeNull();
      expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
    });
  });

  describe("quota exceeded", () => {
    it("handles QuotaExceededError gracefully", () => {
      localStorage.setItem("theme-preference", "dark");

      const originalSetItem = Storage.prototype.setItem;
      let callCount = 0;
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
        this: Storage,
        key: string,
        value: string,
      ) {
        callCount++;
        // Fail on first setItem call (the migration write)
        if (callCount === 1) {
          const err = new DOMException(
            "The quota has been exceeded.",
            "QuotaExceededError",
          );
          throw err;
        }
        originalSetItem.call(this, key, value);
      });

      const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

      // Should not throw
      migrateLocalStorage();

      expect(warnSpy).toHaveBeenCalledWith(
        expect.stringContaining("Quota exceeded"),
      );
    });

    it("does not lose data when concurrent removal causes re-read to return null", () => {
      localStorage.setItem("theme-preference", "dark");

      const originalSetItem = Storage.prototype.setItem;
      const originalGetItem = Storage.prototype.getItem;
      let setCallCount = 0;
      const getCallCounts: Record<string, number> = {};

      vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (
        this: Storage,
        key: string,
      ) {
        getCallCounts[key] = (getCallCounts[key] || 0) + 1;
        // On second getItem call for the V5 key, simulate concurrent removal
        if (key === "theme-preference" && getCallCounts[key] > 1) {
          return null;
        }
        return originalGetItem.call(this, key);
      });

      vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
        this: Storage,
        key: string,
        value: string,
      ) {
        setCallCount++;
        // Fail on first setItem call (the migration write to V6 key)
        if (setCallCount === 1) {
          throw new DOMException(
            "The quota has been exceeded.",
            "QuotaExceededError",
          );
        }
        originalSetItem.call(this, key, value);
      });

      vi.spyOn(console, "warn").mockImplementation(() => {});

      migrateLocalStorage();

      // Data must NOT be silently lost — V6 key should have the original value
      expect(localStorage.getItem("cortex:theme")).toBe("dark");
      // V5 key should be removed
      expect(localStorage.getItem("theme-preference")).toBeNull();
    });
  });

  describe("localStorage unavailable", () => {
    it("does not crash when getItem throws", () => {
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });

      vi.spyOn(console, "warn").mockImplementation(() => {});

      // Should not throw
      expect(() => migrateLocalStorage()).not.toThrow();
    });
  });

  describe("unknown future version", () => {
    it("does not run migration for version > current", () => {
      localStorage.setItem(VERSION_KEY, "99");
      localStorage.setItem("theme-preference", "dark");

      migrateLocalStorage();

      // V5 key not touched
      expect(localStorage.getItem("theme-preference")).toBe("dark");
      // Version not changed
      expect(localStorage.getItem(VERSION_KEY)).toBe("99");
    });
  });

  describe("key value preservation", () => {
    it("preserves exact theme value", () => {
      localStorage.setItem("theme-preference", "dark");

      migrateLocalStorage();

      expect(localStorage.getItem("cortex:theme")).toBe("dark");
    });

    it("preserves exact JSON for recent assignees", () => {
      const data = JSON.stringify(["Alice", "Bob"]);
      localStorage.setItem("beads-recent-assignees", data);

      migrateLocalStorage();

      expect(localStorage.getItem("cortex:recent-assignees")).toBe(data);
    });

    it("preserves exact font family value", () => {
      localStorage.setItem("terminal-font-family", "Fira Code");

      migrateLocalStorage();

      expect(localStorage.getItem("cortex:terminal-font-family")).toBe(
        "Fira Code",
      );
    });

    it("preserves exact font size value", () => {
      localStorage.setItem("terminal-font-size", "16");

      migrateLocalStorage();

      expect(localStorage.getItem("cortex:terminal-font-size")).toBe("16");
    });
  });

  describe("getStorageVersion", () => {
    it("returns null when no version key", () => {
      expect(getStorageVersion()).toBeNull();
    });

    it("returns stored version", () => {
      localStorage.setItem(VERSION_KEY, "6");
      expect(getStorageVersion()).toBe("6");
    });

    it("returns null when localStorage throws", () => {
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("SecurityError");
      });

      expect(getStorageVersion()).toBeNull();
    });
  });

  describe("V5_TO_V6_KEY_MAP", () => {
    it("maps all expected V5 keys", () => {
      expect(V5_TO_V6_KEY_MAP).toEqual({
        "theme-preference": "cortex:theme",
        "beads-recent-assignees": "cortex:recent-assignees",
        "terminal-font-family": "cortex:terminal-font-family",
        "terminal-font-size": "cortex:terminal-font-size",
      });
    });
  });

  describe("V6 → V7 migration", () => {
    const TEST_UUID = "550e8400-e29b-41d4-a716-446655440000";

    function setupCachedConfig(wsName: string, wsId: string): void {
      localStorage.setItem(
        "cortex:config:backend",
        JSON.stringify({
          id: wsId,
          name: wsName,
          workspaces: [{ id: wsId, name: wsName }],
        }),
      );
    }

    it("migrates all workspace-scoped keys to scoped namespace", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      // Set all workspace-scoped keys
      localStorage.setItem("workspace-tree-collapsed", "true");
      localStorage.setItem("workspace-tree-active-filter", "all");
      localStorage.setItem("workspace-tree-repo-collapsed", '{"alpha":true}');
      localStorage.setItem("loom-selected-repos", '["repo-a"]');
      localStorage.setItem("graph-show-closed", "false");
      localStorage.setItem("graph-status-filter", "open");
      localStorage.setItem("graph-dep-type-filter", '["blocking"]');

      migrateLocalStorage();

      // Scoped keys exist
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-collapsed`)).toBe(
        "true",
      );
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-active-filter`)).toBe(
        "all",
      );
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:tree-repo-collapsed`),
      ).toBe('{"alpha":true}');
      expect(localStorage.getItem(`loom:${TEST_UUID}:selected-repos`)).toBe(
        '["repo-a"]',
      );
      expect(localStorage.getItem(`loom:${TEST_UUID}:graph-show-closed`)).toBe(
        "false",
      );
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:graph-status-filter`),
      ).toBe("open");
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:graph-dep-type-filter`),
      ).toBe('["blocking"]');

      // Old global keys removed
      expect(localStorage.getItem("workspace-tree-collapsed")).toBeNull();
      expect(localStorage.getItem("loom-selected-repos")).toBeNull();
      expect(localStorage.getItem("graph-show-closed")).toBeNull();

      // Active workspace renamed to last-workspace-id with UUID
      expect(localStorage.getItem("loom:last-workspace-id")).toBe(TEST_UUID);
      expect(localStorage.getItem("loom-active-workspace")).toBeNull();

      // Version stamped
      expect(localStorage.getItem(VERSION_KEY)).toBe("7");
    });

    it("migrates only existing keys (no phantom keys created)", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      // Only set one key
      localStorage.setItem("workspace-tree-collapsed", "true");

      migrateLocalStorage();

      // Only the key that existed gets migrated
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-collapsed`)).toBe(
        "true",
      );
      // Other scoped keys should not exist
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:tree-active-filter`),
      ).toBeNull();
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:selected-repos`),
      ).toBeNull();
    });

    it("falls back gracefully when no cached config", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      // No cortex:config:backend set

      localStorage.setItem("workspace-tree-collapsed", "true");
      localStorage.setItem("graph-show-closed", "false");

      migrateLocalStorage();

      // Old keys removed (no UUID resolution, so they're cleaned up)
      expect(localStorage.getItem("workspace-tree-collapsed")).toBeNull();
      expect(localStorage.getItem("graph-show-closed")).toBeNull();

      // No scoped keys created (no UUID to namespace with)
      // We can't check for a specific UUID, but no loom:*:* keys should exist
      // Active workspace removed
      expect(localStorage.getItem("loom-active-workspace")).toBeNull();
      expect(localStorage.getItem("loom:last-workspace-id")).toBeNull();

      // Version still stamped
      expect(localStorage.getItem(VERSION_KEY)).toBe("7");
    });

    it("migrates workspace-tree-epic-collapsed:{name} keys", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      localStorage.setItem(
        "workspace-tree-epic-collapsed:my-workspace",
        '{"epic-1":true}',
      );

      migrateLocalStorage();

      // Migrated to scoped key
      expect(
        localStorage.getItem(`loom:${TEST_UUID}:tree-epic-collapsed`),
      ).toBe('{"epic-1":true}');

      // Old key removed
      expect(
        localStorage.getItem("workspace-tree-epic-collapsed:my-workspace"),
      ).toBeNull();
    });

    it("removes epic-collapsed keys with unresolvable workspace names", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      localStorage.setItem(
        "workspace-tree-epic-collapsed:unknown-ws",
        '{"epic-1":true}',
      );

      migrateLocalStorage();

      // Old key removed even though it couldn't be resolved
      expect(
        localStorage.getItem("workspace-tree-epic-collapsed:unknown-ws"),
      ).toBeNull();
    });

    it("is idempotent — running on version 7 is a no-op", () => {
      localStorage.setItem(VERSION_KEY, "7");
      localStorage.setItem(`loom:${TEST_UUID}:tree-collapsed`, "true");

      const setItemSpy = vi.spyOn(Storage.prototype, "setItem");

      migrateLocalStorage();

      // No writes should happen
      expect(setItemSpy).not.toHaveBeenCalled();

      // Data unchanged
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-collapsed`)).toBe(
        "true",
      );
    });

    it("does not overwrite existing scoped key (partial migration recovery)", () => {
      localStorage.setItem(VERSION_KEY, "6");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      // Simulate partial migration: old key exists AND scoped key already written
      localStorage.setItem("workspace-tree-collapsed", "true");
      localStorage.setItem(`loom:${TEST_UUID}:tree-collapsed`, "false");

      migrateLocalStorage();

      // Scoped key preserved (not overwritten)
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-collapsed`)).toBe(
        "false",
      );
      // Old key still removed
      expect(localStorage.getItem("workspace-tree-collapsed")).toBeNull();
    });

    it("V5 keys migrate correctly through V5→V6→V7 chain", () => {
      // Start from V5 (no version stamp)
      localStorage.setItem("theme-preference", "dark");
      localStorage.setItem("workspace-tree-collapsed", "true");
      localStorage.setItem("loom-active-workspace", "my-workspace");
      setupCachedConfig("my-workspace", TEST_UUID);

      migrateLocalStorage();

      // V5→V6 migration happened
      expect(localStorage.getItem("cortex:theme")).toBe("dark");
      expect(localStorage.getItem("theme-preference")).toBeNull();

      // V6→V7 migration happened
      expect(localStorage.getItem(`loom:${TEST_UUID}:tree-collapsed`)).toBe(
        "true",
      );
      expect(localStorage.getItem("workspace-tree-collapsed")).toBeNull();

      // Version stamped at 7
      expect(localStorage.getItem(VERSION_KEY)).toBe("7");
    });
  });

  describe("V6_TO_V7_SCOPED_KEYS", () => {
    it("maps all expected workspace-scoped keys", () => {
      expect(V6_TO_V7_SCOPED_KEYS).toEqual({
        "workspace-tree-collapsed": "tree-collapsed",
        "workspace-tree-active-filter": "tree-active-filter",
        "workspace-tree-repo-collapsed": "tree-repo-collapsed",
        "workspace-tree-work-queue-expanded": "work-queue-expanded",
        "agents-sidebar-collapsed": "agents-sidebar-collapsed",
        "agents-sidebar-work-queue-expanded":
          "agents-sidebar-work-queue-expanded",
        "agents-sidebar-repo-groups-collapsed":
          "agents-sidebar-repo-groups-collapsed",
        "agents-sidebar-ws-collapsed": "agents-sidebar-ws-collapsed",
        "graph-show-closed": "graph-show-closed",
        "graph-status-filter": "graph-status-filter",
        "graph-dep-type-filter": "graph-dep-type-filter",
        "loom-selected-repos": "selected-repos",
      });
    });
  });
});
