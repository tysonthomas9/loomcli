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
    it("stamps version 6 when localStorage is empty", () => {
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
    it("no-ops when version is already 6", () => {
      localStorage.setItem(VERSION_KEY, "6");
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
      // Set V5 key with a known value
      localStorage.setItem("theme-preference", "dark");

      const originalSetItem = Storage.prototype.setItem;
      const originalGetItem = Storage.prototype.getItem;
      let setCallCount = 0;
      let getCallCount = 0;

      // Track getItem calls for "theme-preference" specifically
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (
        this: Storage,
        key: string,
      ) {
        if (key === "theme-preference") {
          getCallCount++;
          // First call returns the real value; subsequent calls return null
          // (simulating concurrent removal)
          if (getCallCount === 1) {
            return originalGetItem.call(this, key);
          }
          return null;
        }
        return originalGetItem.call(this, key);
      });

      // Make the first setItem call throw QuotaExceeded
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
        this: Storage,
        key: string,
        value: string,
      ) {
        setCallCount++;
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

      // The V6 key should have the original value — data must NOT be lost
      expect(localStorage.getItem("cortex:theme")).toBe("dark");
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
});
