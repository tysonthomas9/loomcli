/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTheme hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useTheme } from "../useTheme";

const STORAGE_KEY = "theme-preference";

// Helper to create a mock matchMedia with controllable listeners
function createMockMatchMedia(prefersDark: boolean) {
  const listeners: Array<(e: MediaQueryListEvent) => void> = [];

  const mql = {
    matches: prefersDark,
    media: "(prefers-color-scheme: dark)",
    addEventListener: vi.fn((event: string, handler: (e: MediaQueryListEvent) => void) => {
      if (event === "change") {
        listeners.push(handler);
      }
    }),
    removeEventListener: vi.fn((event: string, handler: (e: MediaQueryListEvent) => void) => {
      if (event === "change") {
        const idx = listeners.indexOf(handler);
        if (idx >= 0) listeners.splice(idx, 1);
      }
    }),
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  };

  const matchMediaMock = vi.fn().mockReturnValue(mql);

  return { matchMediaMock, mql, listeners };
}

describe("useTheme", () => {
  let originalMatchMedia: typeof window.matchMedia;

  beforeEach(() => {
    // Clear localStorage and data-theme attribute before each test
    localStorage.clear();
    delete document.documentElement.dataset.theme;
    originalMatchMedia = window.matchMedia;
  });

  afterEach(() => {
    window.matchMedia = originalMatchMedia;
  });

  describe("initial state", () => {
    it("uses localStorage value when available (light)", () => {
      localStorage.setItem(STORAGE_KEY, "light");
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      expect(result.current.theme).toBe("light");
    });

    it("uses localStorage value when available (dark)", () => {
      localStorage.setItem(STORAGE_KEY, "dark");
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      expect(result.current.theme).toBe("dark");
    });

    it("falls back to OS preference (dark) when no localStorage value", () => {
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      expect(result.current.theme).toBe("dark");
    });

    it("falls back to OS preference (light) when no localStorage value", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      expect(result.current.theme).toBe("light");
    });

    it("ignores invalid localStorage values and falls back to OS preference", () => {
      localStorage.setItem(STORAGE_KEY, "invalid-theme");
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      expect(result.current.theme).toBe("dark");
    });
  });

  describe("data-theme attribute", () => {
    it("sets data-theme on document.documentElement on mount", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      renderHook(() => useTheme());

      expect(document.documentElement.dataset.theme).toBe("light");
    });

    it("updates data-theme when theme changes via toggle", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(document.documentElement.dataset.theme).toBe("light");

      act(() => {
        result.current.toggleTheme();
      });

      expect(document.documentElement.dataset.theme).toBe("dark");
    });

    it("updates data-theme when theme changes via setTheme", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(document.documentElement.dataset.theme).toBe("light");

      act(() => {
        result.current.setTheme("dark");
      });

      expect(document.documentElement.dataset.theme).toBe("dark");
    });
  });

  describe("toggleTheme", () => {
    it("switches from light to dark", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(result.current.theme).toBe("light");

      act(() => {
        result.current.toggleTheme();
      });

      expect(result.current.theme).toBe("dark");
    });

    it("switches from dark to light", () => {
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(result.current.theme).toBe("dark");

      act(() => {
        result.current.toggleTheme();
      });

      expect(result.current.theme).toBe("light");
    });

    it("persists to localStorage on toggle", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      act(() => {
        result.current.toggleTheme();
      });

      expect(localStorage.getItem(STORAGE_KEY)).toBe("dark");
    });

    it("toggles multiple times correctly", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(result.current.theme).toBe("light");

      act(() => {
        result.current.toggleTheme();
      });
      expect(result.current.theme).toBe("dark");

      act(() => {
        result.current.toggleTheme();
      });
      expect(result.current.theme).toBe("light");

      act(() => {
        result.current.toggleTheme();
      });
      expect(result.current.theme).toBe("dark");
    });
  });

  describe("setTheme", () => {
    it("sets theme to dark", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      act(() => {
        result.current.setTheme("dark");
      });

      expect(result.current.theme).toBe("dark");
    });

    it("sets theme to light", () => {
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      act(() => {
        result.current.setTheme("light");
      });

      expect(result.current.theme).toBe("light");
    });

    it("persists to localStorage on setTheme", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      act(() => {
        result.current.setTheme("dark");
      });

      expect(localStorage.getItem(STORAGE_KEY)).toBe("dark");
    });
  });

  describe("OS theme change listener", () => {
    it("responds to OS theme changes when no explicit preference", () => {
      const { matchMediaMock, listeners } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(result.current.theme).toBe("light");

      // Simulate OS theme change to dark
      act(() => {
        for (const listener of listeners) {
          listener({ matches: true } as MediaQueryListEvent);
        }
      });

      expect(result.current.theme).toBe("dark");
    });

    it("does NOT respond to OS theme changes when explicit preference is saved", () => {
      localStorage.setItem(STORAGE_KEY, "light");
      const { matchMediaMock, listeners } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());
      expect(result.current.theme).toBe("light");

      // Simulate OS theme change to dark -- should be ignored
      act(() => {
        for (const listener of listeners) {
          listener({ matches: true } as MediaQueryListEvent);
        }
      });

      expect(result.current.theme).toBe("light");
    });

    it("stops listening to OS changes after user toggles theme", () => {
      const { matchMediaMock, mql, listeners } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      // Listener should be registered initially (no explicit preference)
      expect(mql.addEventListener).toHaveBeenCalledWith("change", expect.any(Function));

      // User explicitly sets a theme
      act(() => {
        result.current.toggleTheme();
      });

      // After toggling, OS changes should not affect the theme
      act(() => {
        for (const listener of listeners) {
          listener({ matches: true } as MediaQueryListEvent);
        }
      });

      // Theme should remain as toggled (dark), not respond to OS
      expect(result.current.theme).toBe("dark");
    });

    it("cleans up listener on unmount", () => {
      const { matchMediaMock, mql } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { unmount } = renderHook(() => useTheme());

      unmount();

      expect(mql.removeEventListener).toHaveBeenCalledWith("change", expect.any(Function));
    });
  });

  describe("localStorage error handling", () => {
    it("handles localStorage.getItem throwing (private browsing)", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const getItemSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("SecurityError: localStorage not available");
      });

      const { result } = renderHook(() => useTheme());

      // Should fall back to OS preference
      expect(result.current.theme).toBe("light");

      getItemSpy.mockRestore();
    });

    it("handles localStorage.setItem throwing on toggle", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      const setItemSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

      // Should not throw
      act(() => {
        result.current.toggleTheme();
      });

      expect(result.current.theme).toBe("dark");

      setItemSpy.mockRestore();
    });

    it("handles localStorage.setItem throwing on setTheme", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock;

      const { result } = renderHook(() => useTheme());

      const setItemSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

      // Should not throw
      act(() => {
        result.current.setTheme("dark");
      });

      expect(result.current.theme).toBe("dark");

      setItemSpy.mockRestore();
    });
  });
});
