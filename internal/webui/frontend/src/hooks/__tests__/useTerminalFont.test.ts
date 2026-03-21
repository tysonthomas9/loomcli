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
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
} from "../useTerminalFont";

const KEY_FAMILY = "cortex:terminal-font-family";
const KEY_SIZE = "cortex:terminal-font-size";

describe("useTerminalFont", () => {
  beforeEach(() => {
    localStorage.clear();
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
});
