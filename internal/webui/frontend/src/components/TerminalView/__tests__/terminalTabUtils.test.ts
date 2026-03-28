/**
 * Unit tests for terminalTabUtils helper functions.
 */

import { describe, it, expect } from "vitest";

import {
  extractBaseName,
  generateTabName,
  getBackendFromSessionName,
  getNextDuplicateName,
  sanitizeSessionName,
  BACKEND_BRAND_COLORS,
  MAX_TABS,
  type TabState,
} from "../terminalTabUtils";

/** Helper to create a minimal TabState for testing. */
function makeTab(overrides: Partial<TabState> & { label: string }): TabState {
  return {
    id: overrides.id ?? overrides.label,
    label: overrides.label,
    sessionName: overrides.sessionName ?? overrides.label,
    connectionState: overrides.connectionState ?? "connected",
    backendName: overrides.backendName ?? "claude",
  };
}

describe("extractBaseName", () => {
  it('returns label unchanged when no counter suffix: "Claude"', () => {
    expect(extractBaseName("Claude")).toBe("Claude");
  });

  it('strips " (2)" suffix: "Claude (2)" -> "Claude"', () => {
    expect(extractBaseName("Claude (2)")).toBe("Claude");
  });

  it('strips " (3)" suffix: "Claude (3)" -> "Claude"', () => {
    expect(extractBaseName("Claude (3)")).toBe("Claude");
  });

  it('does not strip non-counter patterns: "lead-claude-1" unchanged', () => {
    expect(extractBaseName("lead-claude-1")).toBe("lead-claude-1");
  });

  it('handles large counters: "Tab (99)" -> "Tab"', () => {
    expect(extractBaseName("Tab (99)")).toBe("Tab");
  });

  it('does not strip mid-string parens: "My (cool) Tab" unchanged', () => {
    expect(extractBaseName("My (cool) Tab")).toBe("My (cool) Tab");
  });
});

describe("getNextDuplicateName", () => {
  it("returns label with (2) for first duplicate when no existing duplicates", () => {
    const existing = [makeTab({ label: "Claude" })];
    const result = getNextDuplicateName("Claude", existing);
    expect(result).toEqual({
      label: "Claude (2)",
      sessionName: "Claude-2",
    });
  });

  it("returns sequential counter (3) when (2) already exists", () => {
    const existing = [
      makeTab({ label: "Claude" }),
      makeTab({ label: "Claude (2)" }),
    ];
    const result = getNextDuplicateName("Claude", existing);
    expect(result).toEqual({
      label: "Claude (3)",
      sessionName: "Claude-3",
    });
  });

  it("extracts base name from source label with counter suffix", () => {
    const existing = [
      makeTab({ label: "Claude" }),
      makeTab({ label: "Claude (2)" }),
    ];
    // Duplicating "Claude (2)" should extract base "Claude" and find max counter
    const result = getNextDuplicateName("Claude (2)", existing);
    expect(result).toEqual({
      label: "Claude (3)",
      sessionName: "Claude-3",
    });
  });

  it("returns null when MAX_TABS is reached", () => {
    const existing = Array.from({ length: MAX_TABS }, (_, i) =>
      makeTab({ label: `Tab ${i + 1}` }),
    );
    expect(existing).toHaveLength(MAX_TABS);
    const result = getNextDuplicateName("Tab 1", existing);
    expect(result).toBeNull();
  });

  it("uses max+1 counter, does not fill gaps", () => {
    const existing = [
      makeTab({ label: "Claude" }),
      makeTab({ label: "Claude (2)" }),
      makeTab({ label: "Claude (3)" }),
      makeTab({ label: "Claude (5)" }),
    ];
    // Max counter is 5, so next should be 6 (not 4)
    const result = getNextDuplicateName("Claude", existing);
    expect(result).toEqual({
      label: "Claude (6)",
      sessionName: "Claude-6",
    });
  });

  it("sanitizes session name, replacing spaces with dashes", () => {
    const existing = [makeTab({ label: "My Terminal" })];
    const result = getNextDuplicateName("My Terminal", existing);
    expect(result).toEqual({
      label: "My Terminal (2)",
      sessionName: "My-Terminal-2",
    });
  });

  it("works with single tab existing (no counter siblings)", () => {
    const existing = [makeTab({ label: "Codex" })];
    const result = getNextDuplicateName("Codex", existing);
    expect(result).toEqual({
      label: "Codex (2)",
      sessionName: "Codex-2",
    });
  });
});

describe("generateTabName (shell backend)", () => {
  it('generates "lead-shell-1" when no existing shell tabs', () => {
    const result = generateTabName("shell", []);
    expect(result.sessionName).toBe("lead-shell-1");
    expect(result.label).toBe("lead-shell-1");
  });

  it('generates "lead-shell-2" when lead-shell-1 exists', () => {
    const existing = [
      makeTab({ sessionName: "lead-shell-1", label: "Terminal" }),
    ];
    const result = generateTabName("shell", existing);
    expect(result.sessionName).toBe("lead-shell-2");
  });

  it("increments correctly with multiple shell tabs", () => {
    const existing = [
      makeTab({ sessionName: "lead-shell-1", label: "Terminal" }),
      makeTab({ sessionName: "lead-shell-3", label: "Terminal (3)" }),
    ];
    const result = generateTabName("shell", existing);
    expect(result.sessionName).toBe("lead-shell-4");
  });

  it("ignores non-shell tabs when counting", () => {
    const existing = [
      makeTab({ sessionName: "lead-claude-1", label: "Claude" }),
      makeTab({ sessionName: "lead-shell-1", label: "Terminal" }),
    ];
    const result = generateTabName("shell", existing);
    expect(result.sessionName).toBe("lead-shell-2");
  });

  it("prefixes session name with workspace", () => {
    const result = generateTabName("claude", [], "my-workspace");
    expect(result.sessionName).toBe("my-workspace--lead-claude-1");
    expect(result.label).toBe("lead-claude-1");
  });

  it("does not prefix for default workspace", () => {
    const result = generateTabName("claude", [], "default");
    expect(result.sessionName).toBe("lead-claude-1");
    expect(result.label).toBe("lead-claude-1");
  });
});

describe("getBackendFromSessionName (shell backend)", () => {
  it('returns "shell" for "lead-shell-1"', () => {
    expect(getBackendFromSessionName("lead-shell-1")).toBe("shell");
  });

  it('returns "shell" for "lead-shell-5"', () => {
    expect(getBackendFromSessionName("lead-shell-5")).toBe("shell");
  });

  it('returns "claude" for "lead-claude-1" (non-shell)', () => {
    expect(getBackendFromSessionName("lead-claude-1")).toBe("claude");
  });

  it("returns default for non-matching session name", () => {
    expect(getBackendFromSessionName("talk-to-lead", "claude")).toBe("claude");
  });
});

describe("BACKEND_BRAND_COLORS (shell)", () => {
  it('includes "shell" with gray color', () => {
    expect(BACKEND_BRAND_COLORS).toHaveProperty("shell");
    expect(BACKEND_BRAND_COLORS.shell).toBe("#6b7280");
  });

  it("has entries for all known backends", () => {
    expect(BACKEND_BRAND_COLORS).toHaveProperty("claude");
    expect(BACKEND_BRAND_COLORS).toHaveProperty("codex");
    expect(BACKEND_BRAND_COLORS).toHaveProperty("opencode");
    expect(BACKEND_BRAND_COLORS).toHaveProperty("shell");
  });
});

describe("TabState agentName field", () => {
  it("allows optional agentName on TabState", () => {
    const tab: TabState = {
      id: "agent-fox",
      label: "agent-fox",
      sessionName: "agent-fox",
      connectionState: "connected",
      backendName: "agent",
      agentName: "fox",
    };
    expect(tab.agentName).toBe("fox");
  });

  it("agentName is undefined by default", () => {
    const tab: TabState = makeTab({ label: "Normal Tab" });
    expect(tab.agentName).toBeUndefined();
  });
});

describe("sanitizeSessionName (agent names)", () => {
  it("replaces dots with dashes", () => {
    expect(sanitizeSessionName("agent.alpha")).toBe("agent-alpha");
  });

  it("strips special characters", () => {
    expect(sanitizeSessionName("agent@123!")).toBe("agent123");
  });

  it("preserves hyphens and underscores", () => {
    expect(sanitizeSessionName("agent-alpha_1")).toBe("agent-alpha_1");
  });

  it("handles multiple dots", () => {
    expect(sanitizeSessionName("a.b.c.d")).toBe("a-b-c-d");
  });

  it("returns empty string for all-special input", () => {
    expect(sanitizeSessionName("@#$!")).toBe("");
  });
});
