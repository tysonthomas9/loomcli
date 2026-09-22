/**
 * @vitest-environment jsdom
 */

import { describe, it, expect } from "vitest";

import {
  isUserFacingBackend,
  toBackendInfo,
  KNOWN_BACKEND_DEFAULTS,
} from "../backendDefaults";

describe("backendDefaults", () => {
  describe("isUserFacingBackend", () => {
    it("hides Local Dogfood unless testing backends are enabled", () => {
      expect(isUserFacingBackend("localdogfood", false)).toBe(false);
      expect(isUserFacingBackend("local-dogfood", false)).toBe(false);
      expect(isUserFacingBackend("local_dogfood", false)).toBe(false);
      expect(isUserFacingBackend("localdogfood", true)).toBe(true);
      expect(isUserFacingBackend("codex", false)).toBe(true);
    });
  });

  describe("KNOWN_BACKEND_DEFAULTS", () => {
    it("has defaults for claude, codex, opencode, gemini, cursor, browser, and shell", () => {
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("claude");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("codex");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("opencode");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("gemini");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("cursor");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("browser");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("shell");
    });

    it('has shell with displayName "Terminal", provider "System", brandColor "#6b7280"', () => {
      const shell = KNOWN_BACKEND_DEFAULTS.shell;
      expect(shell.displayName).toBe("Terminal");
      expect(shell.provider).toBe("System");
      expect(shell.brandColor).toBe("#6b7280");
    });
  });

  describe("toBackendInfo", () => {
    it("returns known defaults for a recognized backend", () => {
      const info = toBackendInfo("claude");
      expect(info.name).toBe("claude");
      expect(info.displayName).toBe("Claude");
      expect(info.provider).toBe("Anthropic");
      expect(info.brandColor).toBe("#d4a574");
      expect(info.available).toBe(true);
    });

    it("generates fallback values for unknown backends", () => {
      const info = toBackendInfo("custom-llm");
      expect(info.name).toBe("custom-llm");
      expect(info.displayName).toBe("Custom-llm");
      expect(info.provider).toBe("Unknown");
      expect(info.brandColor).toBe("#888888");
      expect(info.available).toBe(true);
    });

    it("merges API data over defaults", () => {
      const info = toBackendInfo("claude", {
        displayName: "Claude 4",
        available: false,
        healthMessage: "Rate limited",
      });
      expect(info.displayName).toBe("Claude 4");
      expect(info.provider).toBe("Anthropic"); // from defaults
      expect(info.available).toBe(false);
      expect(info.healthMessage).toBe("Rate limited");
    });

    it("prefers API data over known defaults", () => {
      const info = toBackendInfo("claude", {
        provider: "Anthropic Inc.",
        brandColor: "#ff0000",
      });
      expect(info.provider).toBe("Anthropic Inc.");
      expect(info.brandColor).toBe("#ff0000");
    });

    it('returns shell defaults: displayName "Terminal", provider "System"', () => {
      const info = toBackendInfo("shell");
      expect(info.name).toBe("shell");
      expect(info.displayName).toBe("Terminal");
      expect(info.provider).toBe("System");
      expect(info.brandColor).toBe("#6b7280");
      expect(info.available).toBe(true);
    });
  });
});
