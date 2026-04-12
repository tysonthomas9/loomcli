/**
 * Unit tests for workspaceColor utility.
 */

import { describe, it, expect } from "vitest";

import { getWorkspaceColor } from "../workspaceColor";

const PALETTE = [
  "#3b82f6", // blue
  "#22c55e", // green
  "#8b5cf6", // purple
  "#f97316", // orange
  "#ec4899", // pink
  "#06b6d4", // cyan
  "#f59e0b", // amber
  "#ef4444", // red
];

describe("getWorkspaceColor", () => {
  it("returns consistent color for the same name", () => {
    const color1 = getWorkspaceColor("my-workspace");
    const color2 = getWorkspaceColor("my-workspace");

    expect(color1).toBe(color2);
  });

  it("is deterministic across multiple calls", () => {
    const results = Array.from({ length: 10 }, () =>
      getWorkspaceColor("stable-name"),
    );

    expect(new Set(results).size).toBe(1);
  });

  it("returns a valid 7-character hex color", () => {
    const color = getWorkspaceColor("test");

    expect(color).toMatch(/^#[0-9A-Fa-f]{6}$/);
    expect(color).toHaveLength(7);
  });

  it("returns a color from the palette", () => {
    const names = [
      "alpha",
      "beta",
      "gamma",
      "delta",
      "epsilon",
      "zeta",
      "my-project",
      "workspace-1",
    ];
    for (const name of names) {
      expect(PALETTE).toContain(getWorkspaceColor(name));
    }
  });

  it("different names can produce different colors", () => {
    // With 8 colors and many different names, at least two should differ
    const names = [
      "aaa",
      "bbb",
      "ccc",
      "ddd",
      "eee",
      "fff",
      "ggg",
      "hhh",
      "iii",
      "zzz",
    ];
    const colors = names.map(getWorkspaceColor);
    const uniqueColors = new Set(colors);

    expect(uniqueColors.size).toBeGreaterThan(1);
  });

  it("handles empty string without throwing", () => {
    const color = getWorkspaceColor("");

    expect(PALETTE).toContain(color);
  });

  it("handles single character names", () => {
    const color = getWorkspaceColor("a");

    expect(PALETTE).toContain(color);
  });

  it("handles moderately long names", () => {
    const longName = "a".repeat(20);
    const color = getWorkspaceColor(longName);

    expect(PALETTE).toContain(color);
  });

  it("handles names with special characters", () => {
    const names = [
      "my-workspace",
      "my_workspace",
      "my workspace",
      "my.workspace",
      "workspace/sub",
      "workspace@2",
    ];
    for (const name of names) {
      expect(PALETTE).toContain(getWorkspaceColor(name));
    }
  });

  it("treats different strings as different inputs", () => {
    const color1 = getWorkspaceColor("abc");
    const color2 = getWorkspaceColor("ABC");

    // They may or may not be equal, but both should be valid palette colors
    expect(PALETTE).toContain(color1);
    expect(PALETTE).toContain(color2);
  });

  it("covers all palette colors with distinct inputs", () => {
    // These names are known to hash to all 8 different palette indices
    const inputs = [
      "gamma",
      "beta",
      "iota",
      "alpha",
      "omicron",
      "h",
      "lambda",
      "delta",
    ];
    const colors = new Set(inputs.map(getWorkspaceColor));

    expect(colors.size).toBe(PALETTE.length);
  });
});
