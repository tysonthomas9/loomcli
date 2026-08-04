/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRecentOwners hook.
 * Tests localStorage persistence of recent owner names.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useRecentOwners } from "../useRecentOwners";

/**
 * Mock localStorage implementation for testing.
 */
function createMockLocalStorage(): {
  store: Map<string, string>;
  getItem: ReturnType<typeof vi.fn>;
  setItem: ReturnType<typeof vi.fn>;
  removeItem: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
} {
  const store = new Map<string, string>();

  return {
    store,
    getItem: vi.fn((key: string) => store.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      store.set(key, value);
    }),
    removeItem: vi.fn((key: string) => {
      store.delete(key);
    }),
    clear: vi.fn(() => {
      store.clear();
    }),
  };
}

describe("useRecentOwners", () => {
  let mockStorage: ReturnType<typeof createMockLocalStorage>;
  let originalLocalStorage: Storage;

  beforeEach(() => {
    // Save original localStorage
    originalLocalStorage = window.localStorage;

    // Create fresh mock storage
    mockStorage = createMockLocalStorage();

    // Replace localStorage with mock
    Object.defineProperty(window, "localStorage", {
      value: {
        getItem: mockStorage.getItem,
        setItem: mockStorage.setItem,
        removeItem: mockStorage.removeItem,
        clear: mockStorage.clear,
        get length() {
          return mockStorage.store.size;
        },
        key: (index: number) => {
          const keys = Array.from(mockStorage.store.keys());
          return keys[index] ?? null;
        },
      },
      writable: true,
      configurable: true,
    });

    vi.clearAllMocks();
  });

  afterEach(() => {
    // Restore original localStorage
    Object.defineProperty(window, "localStorage", {
      value: originalLocalStorage,
      writable: true,
      configurable: true,
    });
  });

  describe("Initial state", () => {
    it("returns empty array when localStorage is empty", () => {
      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toEqual([]);
    });

    it("loads initial state from localStorage", () => {
      mockStorage.store.set(
        "loom-recent-owners",
        JSON.stringify(["Alice", "Bob", "Charlie"]),
      );

      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toEqual(["Alice", "Bob", "Charlie"]);
    });

    it("handles invalid JSON in localStorage gracefully", () => {
      mockStorage.store.set("loom-recent-owners", "not valid json {{{");

      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toEqual([]);
    });

    it("handles non-array JSON in localStorage gracefully", () => {
      mockStorage.store.set(
        "loom-recent-owners",
        JSON.stringify({ invalid: "object" }),
      );

      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toEqual([]);
    });

    it("filters out non-string items from localStorage", () => {
      mockStorage.store.set(
        "loom-recent-owners",
        JSON.stringify(["Alice", 123, "Bob", null, "Charlie"]),
      );

      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toEqual(["Alice", "Bob", "Charlie"]);
    });
  });

  describe("addRecentOwner", () => {
    it("adds a name to the front of the list", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
      });

      expect(result.current.recentOwners).toEqual(["Alice"]);

      act(() => {
        result.current.addRecentOwner("Bob");
      });

      expect(result.current.recentOwners).toEqual(["Bob", "Alice"]);
    });

    it("deduplicates names case-insensitively", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
        result.current.addRecentOwner("Bob");
        result.current.addRecentOwner("ALICE"); // Same as Alice, different case
      });

      // Should have ALICE at front (preserving case of most recent), followed by Bob
      expect(result.current.recentOwners).toEqual(["ALICE", "Bob"]);
    });

    it("preserves the case of the most recent addition", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("alice");
      });
      expect(result.current.recentOwners[0]).toBe("alice");

      act(() => {
        result.current.addRecentOwner("ALICE");
      });
      expect(result.current.recentOwners[0]).toBe("ALICE");

      act(() => {
        result.current.addRecentOwner("Alice");
      });
      expect(result.current.recentOwners[0]).toBe("Alice");
    });

    it("trims the list to max 5 items", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("One");
        result.current.addRecentOwner("Two");
        result.current.addRecentOwner("Three");
        result.current.addRecentOwner("Four");
        result.current.addRecentOwner("Five");
        result.current.addRecentOwner("Six");
      });

      expect(result.current.recentOwners).toHaveLength(5);
      expect(result.current.recentOwners).toEqual([
        "Six",
        "Five",
        "Four",
        "Three",
        "Two",
      ]);
      // 'One' should have been dropped
      expect(result.current.recentOwners).not.toContain("One");
    });

    it("ignores empty strings", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
        result.current.addRecentOwner("");
      });

      expect(result.current.recentOwners).toEqual(["Alice"]);
    });

    it("ignores whitespace-only strings", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
        result.current.addRecentOwner("   ");
        result.current.addRecentOwner("\t\n");
      });

      expect(result.current.recentOwners).toEqual(["Alice"]);
    });

    it("trims whitespace from names", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("  Alice  ");
      });

      expect(result.current.recentOwners).toEqual(["Alice"]);
    });

    it("moves existing name to front when re-added", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
        result.current.addRecentOwner("Bob");
        result.current.addRecentOwner("Charlie");
      });

      expect(result.current.recentOwners).toEqual(["Charlie", "Bob", "Alice"]);

      act(() => {
        result.current.addRecentOwner("Alice");
      });

      expect(result.current.recentOwners).toEqual(["Alice", "Charlie", "Bob"]);
    });
  });

  describe("clearRecentOwners", () => {
    it("clears all recent owners", () => {
      mockStorage.store.set(
        "loom-recent-owners",
        JSON.stringify(["Alice", "Bob", "Charlie"]),
      );

      const { result } = renderHook(() => useRecentOwners());

      expect(result.current.recentOwners).toHaveLength(3);

      act(() => {
        result.current.clearRecentOwners();
      });

      expect(result.current.recentOwners).toEqual([]);
    });

    it("persists the empty state to localStorage", () => {
      mockStorage.store.set(
        "loom-recent-owners",
        JSON.stringify(["Alice", "Bob"]),
      );

      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.clearRecentOwners();
      });

      // Check localStorage was updated to empty array
      expect(mockStorage.setItem).toHaveBeenCalledWith(
        "loom-recent-owners",
        "[]",
      );
    });
  });

  describe("localStorage persistence", () => {
    it("persists added names to localStorage", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
      });

      expect(mockStorage.setItem).toHaveBeenCalledWith(
        "loom-recent-owners",
        JSON.stringify(["Alice"]),
      );
    });

    it("persists multiple names in correct order", () => {
      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
      });

      act(() => {
        result.current.addRecentOwner("Bob");
      });

      // Get the last call to setItem
      const lastCall =
        mockStorage.setItem.mock.calls[
          mockStorage.setItem.mock.calls.length - 1
        ];
      expect(lastCall[0]).toBe("loom-recent-owners");
      expect(JSON.parse(lastCall[1] as string)).toEqual(["Bob", "Alice"]);
    });

    it("survives component remount", () => {
      const { result, unmount } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
        result.current.addRecentOwner("Bob");
      });

      unmount();

      // Render new hook instance
      const { result: result2 } = renderHook(() => useRecentOwners());

      expect(result2.current.recentOwners).toEqual(["Bob", "Alice"]);
    });
  });

  describe("localStorage unavailable", () => {
    it("handles localStorage getItem throwing gracefully", () => {
      mockStorage.getItem.mockImplementation(() => {
        throw new Error("localStorage not available");
      });

      const { result } = renderHook(() => useRecentOwners());

      // Should return empty array instead of crashing
      expect(result.current.recentOwners).toEqual([]);
    });

    it("handles localStorage setItem throwing gracefully", () => {
      mockStorage.setItem.mockImplementation(() => {
        throw new Error("localStorage quota exceeded");
      });

      const { result } = renderHook(() => useRecentOwners());

      // Should still work in memory even if storage fails
      act(() => {
        result.current.addRecentOwner("Alice");
      });

      expect(result.current.recentOwners).toEqual(["Alice"]);
    });

    it("continues working after localStorage error", () => {
      // First call fails
      mockStorage.setItem.mockImplementationOnce(() => {
        throw new Error("First error");
      });

      const { result } = renderHook(() => useRecentOwners());

      act(() => {
        result.current.addRecentOwner("Alice");
      });

      // State should still update
      expect(result.current.recentOwners).toEqual(["Alice"]);

      // Subsequent adds should also work
      act(() => {
        result.current.addRecentOwner("Bob");
      });

      expect(result.current.recentOwners).toEqual(["Bob", "Alice"]);
    });
  });

  describe("return value stability", () => {
    it("returns stable function references", () => {
      const { result, rerender } = renderHook(() => useRecentOwners());

      const addFn1 = result.current.addRecentOwner;
      const clearFn1 = result.current.clearRecentOwners;

      rerender();

      const addFn2 = result.current.addRecentOwner;
      const clearFn2 = result.current.clearRecentOwners;

      expect(addFn1).toBe(addFn2);
      expect(clearFn1).toBe(clearFn2);
    });
  });
});
