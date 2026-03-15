/**
 * Unit tests for terminalTabUtils helper functions.
 */

import { describe, it, expect } from "vitest";

import {
  extractBaseName,
  getNextDuplicateName,
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
