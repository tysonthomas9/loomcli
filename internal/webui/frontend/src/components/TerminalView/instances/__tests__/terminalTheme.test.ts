/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for getTerminalTheme helper.
 */

import { describe, it, expect, afterEach } from "vitest";

import { getTerminalTheme } from "../terminalTheme";

describe("getTerminalTheme", () => {
  afterEach(() => {
    // Clean up any CSS custom properties set during tests.
    const root = document.documentElement;
    root.style.removeProperty("--terminal-bg");
    root.style.removeProperty("--terminal-fg");
    root.style.removeProperty("--terminal-cursor");
    root.style.removeProperty("--terminal-selection");
  });

  describe("return shape", () => {
    it("returns an object with background, foreground, cursor, and selectionBackground keys", () => {
      const theme = getTerminalTheme();
      expect(theme).toHaveProperty("background");
      expect(theme).toHaveProperty("foreground");
      expect(theme).toHaveProperty("cursor");
      expect(theme).toHaveProperty("selectionBackground");
    });

    it("returns exactly four keys", () => {
      const theme = getTerminalTheme();
      expect(Object.keys(theme)).toHaveLength(4);
    });

    it("returns string values for all properties", () => {
      const theme = getTerminalTheme();
      for (const value of Object.values(theme)) {
        expect(typeof value).toBe("string");
      }
    });
  });

  describe("hardcoded fallback defaults (no CSS variables set)", () => {
    it('falls back to "#1e1e1e" for background', () => {
      const theme = getTerminalTheme();
      expect(theme.background).toBe("#1e1e1e");
    });

    it('falls back to "#d4d4d4" for foreground', () => {
      const theme = getTerminalTheme();
      expect(theme.foreground).toBe("#d4d4d4");
    });

    it('falls back to "#d4d4d4" for cursor', () => {
      const theme = getTerminalTheme();
      expect(theme.cursor).toBe("#d4d4d4");
    });

    it('falls back to "rgba(255, 255, 255, 0.15)" for selectionBackground', () => {
      const theme = getTerminalTheme();
      expect(theme.selectionBackground).toBe("rgba(255, 255, 255, 0.15)");
    });
  });

  describe("reads CSS custom properties when set", () => {
    it("reads --terminal-bg from computed style", () => {
      document.documentElement.style.setProperty("--terminal-bg", "#f5f5f5");

      const theme = getTerminalTheme();
      expect(theme.background).toBe("#f5f5f5");
    });

    it("reads --terminal-fg from computed style", () => {
      document.documentElement.style.setProperty("--terminal-fg", "#1f2937");

      const theme = getTerminalTheme();
      expect(theme.foreground).toBe("#1f2937");
    });

    it("reads --terminal-cursor from computed style", () => {
      document.documentElement.style.setProperty(
        "--terminal-cursor",
        "#1f2937",
      );

      const theme = getTerminalTheme();
      expect(theme.cursor).toBe("#1f2937");
    });

    it("reads --terminal-selection from computed style", () => {
      document.documentElement.style.setProperty(
        "--terminal-selection",
        "rgba(0, 0, 0, 0.1)",
      );

      const theme = getTerminalTheme();
      expect(theme.selectionBackground).toBe("rgba(0, 0, 0, 0.1)");
    });

    it("reads all four variables together (light theme scenario)", () => {
      const root = document.documentElement;
      root.style.setProperty("--terminal-bg", "#f5f5f5");
      root.style.setProperty("--terminal-fg", "#1f2937");
      root.style.setProperty("--terminal-cursor", "#1f2937");
      root.style.setProperty("--terminal-selection", "rgba(0, 0, 0, 0.1)");

      const theme = getTerminalTheme();
      expect(theme).toEqual({
        background: "#f5f5f5",
        foreground: "#1f2937",
        cursor: "#1f2937",
        selectionBackground: "rgba(0, 0, 0, 0.1)",
      });
    });
  });

  describe("whitespace handling", () => {
    it("trims leading/trailing whitespace from CSS variable values", () => {
      document.documentElement.style.setProperty(
        "--terminal-bg",
        "  #222222  ",
      );

      const theme = getTerminalTheme();
      expect(theme.background).toBe("#222222");
    });

    it("falls back to default when variable is set to empty string", () => {
      document.documentElement.style.setProperty("--terminal-bg", "");

      const theme = getTerminalTheme();
      expect(theme.background).toBe("#1e1e1e");
    });
  });
});
