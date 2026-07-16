/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTerminalFont hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  useTerminalFont,
  applyTerminalFont,
  TERMINAL_FONT_CHANGE_EVENT,
  TERMINAL_FONT_FAMILY_VAR,
  TERMINAL_FONT_SIZE_VAR,
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
} from "../useTerminalFont";

const KEY_FAMILY = "cortex:terminal-font-family";
const KEY_SIZE = "cortex:terminal-font-size";

describe("useTerminalFont", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.style.removeProperty(TERMINAL_FONT_FAMILY_VAR);
    document.documentElement.style.removeProperty(TERMINAL_FONT_SIZE_VAR);
  });

  describe("initial state", () => {
    it("returns defaults when localStorage is empty", () => {
      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontFamily).toBe(DEFAULT_FONT_FAMILY);
      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("reads stored fontFamily from localStorage", () => {
      localStorage.setItem(KEY_FAMILY, '"Fira Code", monospace');

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontFamily).toBe('"Fira Code", monospace');
    });

    it("reads stored fontSize from localStorage", () => {
      localStorage.setItem(KEY_SIZE, "18");

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontSize).toBe(18);
    });
  });

  describe("setFontFamily", () => {
    it("updates state and writes to localStorage", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontFamily("Monaco, monospace");
      });

      expect(result.current.fontFamily).toBe("Monaco, monospace");
      expect(localStorage.getItem(KEY_FAMILY)).toBe("Monaco, monospace");
    });

    it("falls back to default when given empty string", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontFamily("");
      });

      expect(result.current.fontFamily).toBe(DEFAULT_FONT_FAMILY);
    });

    it("falls back to default when given whitespace-only string", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontFamily("   ");
      });

      expect(result.current.fontFamily).toBe(DEFAULT_FONT_FAMILY);
    });
  });

  describe("setFontSize", () => {
    it("updates state and writes to localStorage", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(20);
      });

      expect(result.current.fontSize).toBe(20);
      expect(localStorage.getItem(KEY_SIZE)).toBe("20");
    });

    it("falls back to default for NaN", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(NaN);
      });

      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("falls back to default for size below 8", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(5);
      });

      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("falls back to default for size above 72", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(100);
      });

      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("accepts boundary value 8", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(8);
      });

      expect(result.current.fontSize).toBe(8);
    });

    it("accepts boundary value 72", () => {
      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(72);
      });

      expect(result.current.fontSize).toBe(72);
    });
  });

  describe("invalid localStorage values", () => {
    it("falls back to default fontSize for non-numeric stored value", () => {
      localStorage.setItem(KEY_SIZE, "not-a-number");

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("falls back to default fontSize for out-of-range stored value", () => {
      localStorage.setItem(KEY_SIZE, "200");

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);
    });

    it("falls back to default fontFamily for empty stored value", () => {
      localStorage.setItem(KEY_FAMILY, "");

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontFamily).toBe(DEFAULT_FONT_FAMILY);
    });
  });

  describe("localStorage error handling", () => {
    it("handles localStorage.getItem throwing", () => {
      const spy = vi
        .spyOn(Storage.prototype, "getItem")
        .mockImplementation(() => {
          throw new Error("SecurityError");
        });

      const { result } = renderHook(() => useTerminalFont());

      expect(result.current.fontFamily).toBe(DEFAULT_FONT_FAMILY);
      expect(result.current.fontSize).toBe(DEFAULT_FONT_SIZE);

      spy.mockRestore();
    });

    it("handles localStorage.setItem throwing on setFontFamily", () => {
      const spy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("QuotaExceededError");
        });

      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontFamily("Monaco, monospace");
      });

      // State still updates even though localStorage write failed
      expect(result.current.fontFamily).toBe("Monaco, monospace");

      spy.mockRestore();
    });

    it("handles localStorage.setItem throwing on setFontSize", () => {
      const spy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("QuotaExceededError");
        });

      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontSize(20);
      });

      expect(result.current.fontSize).toBe(20);

      spy.mockRestore();
    });
  });

  describe("applyTerminalFont", () => {
    it("sets CSS custom properties on documentElement", () => {
      applyTerminalFont("Monaco, monospace", 16);

      expect(
        document.documentElement.style.getPropertyValue(
          TERMINAL_FONT_FAMILY_VAR,
        ),
      ).toBe("Monaco, monospace");
      expect(
        document.documentElement.style.getPropertyValue(TERMINAL_FONT_SIZE_VAR),
      ).toBe("16px");
    });

    it("applies stored prefs on hook mount", () => {
      localStorage.setItem(KEY_FAMILY, '"Fira Code", monospace');
      localStorage.setItem(KEY_SIZE, "18");

      renderHook(() => useTerminalFont());

      expect(
        document.documentElement.style.getPropertyValue(
          TERMINAL_FONT_FAMILY_VAR,
        ),
      ).toBe('"Fira Code", monospace');
      expect(
        document.documentElement.style.getPropertyValue(TERMINAL_FONT_SIZE_VAR),
      ).toBe("18px");
    });
  });

  describe("font change propagation", () => {
    it("dispatches TERMINAL_FONT_CHANGE_EVENT when font family changes", () => {
      const handler = vi.fn();
      window.addEventListener(TERMINAL_FONT_CHANGE_EVENT, handler);

      const { result } = renderHook(() => useTerminalFont());

      act(() => {
        result.current.setFontFamily("Monaco, monospace");
      });

      expect(handler).toHaveBeenCalledTimes(1);
      expect((handler.mock.calls[0]?.[0] as CustomEvent).detail).toEqual({
        fontFamily: "Monaco, monospace",
        fontSize: DEFAULT_FONT_SIZE,
      });

      window.removeEventListener(TERMINAL_FONT_CHANGE_EVENT, handler);
    });

    it("syncs state across hook instances via custom event", () => {
      const { result: settings } = renderHook(() => useTerminalFont());
      const { result: app } = renderHook(() => useTerminalFont());

      act(() => {
        settings.current.setFontSize(20);
      });

      expect(app.current.fontSize).toBe(20);
    });
  });
});
