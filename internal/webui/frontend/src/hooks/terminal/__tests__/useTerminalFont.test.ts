/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTerminalFont hook.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  useTerminalFont,
  applyTerminalFont,
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
});
