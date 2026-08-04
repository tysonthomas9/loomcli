/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  migrateLocalStorage,
  getStorageVersion,
  CURRENT_VERSION,
  VERSION_KEY,
} from "../migrateLocalStorage";

describe("migrateLocalStorage", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("stamps current version when localStorage is empty", () => {
    migrateLocalStorage();

    expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
  });

  it("no-ops when a version is already present", () => {
    localStorage.setItem(VERSION_KEY, CURRENT_VERSION);

    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");

    migrateLocalStorage();

    expect(setItemSpy).not.toHaveBeenCalled();
    expect(localStorage.getItem(VERSION_KEY)).toBe(CURRENT_VERSION);
  });

  it("does not downgrade future versions", () => {
    localStorage.setItem(VERSION_KEY, "99");

    migrateLocalStorage();

    expect(localStorage.getItem(VERSION_KEY)).toBe("99");
  });

  it("does not crash when localStorage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });

    vi.spyOn(console, "warn").mockImplementation(() => {});

    expect(() => migrateLocalStorage()).not.toThrow();
  });
});

describe("getStorageVersion", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns null when no version key exists", () => {
    expect(getStorageVersion()).toBeNull();
  });

  it("returns stored version", () => {
    localStorage.setItem(VERSION_KEY, "7");

    expect(getStorageVersion()).toBe("7");
  });

  it("returns null when localStorage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });

    expect(getStorageVersion()).toBeNull();
  });
});
