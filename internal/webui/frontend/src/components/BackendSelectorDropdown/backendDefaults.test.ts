/**
 * @vitest-environment jsdom
 */

import { describe, it, expect } from "vitest";

import { toBackendInfo, KNOWN_BACKEND_DEFAULTS } from "./backendDefaults";

describe("backendDefaults", () => {
  describe("KNOWN_BACKEND_DEFAULTS", () => {
    it("has defaults for claude, codex, and opencode", () => {
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("claude");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("codex");
      expect(KNOWN_BACKEND_DEFAULTS).toHaveProperty("opencode");
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
  });
});
