/**
 * Unit tests for colorUtils.
 */

import { describe, it, expect } from "vitest";

import {
  AVATAR_COLORS,
  getAvatarColor,
  shouldUseWhiteText,
} from "../colorUtils";

describe("AVATAR_COLORS", () => {
  it("contains 8 entries", () => {
    expect(AVATAR_COLORS).toHaveLength(8);
  });

  it("all entries are valid 7-character hex color strings", () => {
    const hexPattern = /^#[0-9A-Fa-f]{6}$/;
    for (const color of AVATAR_COLORS) {
      expect(color).toMatch(hexPattern);
    }
  });
});

describe("getAvatarColor", () => {
  it("returns consistent color for the same name", () => {
    const color1 = getAvatarColor("falcon");
    const color2 = getAvatarColor("falcon");

    expect(color1).toBe(color2);
  });

  it("returns a valid 7-character hex color", () => {
    const color = getAvatarColor("nova");

    expect(color).toMatch(/^#[0-9A-Fa-f]{6}$/);
    expect(color).toHaveLength(7);
  });

  it("returns a palette color for empty string", () => {
    const color = getAvatarColor("");

    expect(AVATAR_COLORS).toContain(color);
  });

  it("returns a color from the AVATAR_COLORS palette", () => {
    const names = ["alpha", "beta", "gamma", "delta", "epsilon"];
    for (const name of names) {
      expect(AVATAR_COLORS).toContain(getAvatarColor(name));
    }
  });

  it("different names can produce different colors", () => {
    const color1 = getAvatarColor("aaa");
    const color2 = getAvatarColor("zzz");

    expect(color1).not.toBe(color2);
  });
});

describe("shouldUseWhiteText", () => {
  it("returns true for black (#000000)", () => {
    expect(shouldUseWhiteText("#000000")).toBe(true);
  });

  it("returns true for dark gray (#333333)", () => {
    expect(shouldUseWhiteText("#333333")).toBe(true);
  });

  it("returns false for white (#FFFFFF)", () => {
    expect(shouldUseWhiteText("#FFFFFF")).toBe(false);
  });

  it("returns false for light pastel sage green (#9DC08B)", () => {
    expect(shouldUseWhiteText("#9DC08B")).toBe(false);
  });

  it("returns false for light pastel peach (#F59E87)", () => {
    expect(shouldUseWhiteText("#F59E87")).toBe(false);
  });

  it("returns true for all AVATAR_COLORS (they are saturated/dark)", () => {
    for (const color of AVATAR_COLORS) {
      expect(shouldUseWhiteText(color)).toBe(true);
    }
  });

  it("returns true for very dark blue (#0a0a2e)", () => {
    expect(shouldUseWhiteText("#0a0a2e")).toBe(true);
  });

  it("returns true for dark red (#4a0000)", () => {
    expect(shouldUseWhiteText("#4a0000")).toBe(true);
  });
});
