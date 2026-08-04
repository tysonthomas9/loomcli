/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for formatIssueId utility.
 */

import { describe, it, expect, vi } from "vitest";

import { formatIssueId } from "../formatIssueId";

describe("formatIssueId", () => {
  it('returns "unknown" for empty string', () => {
    expect(formatIssueId("")).toBe("unknown");
  });

  it("returns short ID as-is when length <= 16", () => {
    expect(formatIssueId("issue-v3vw")).toBe("issue-v3vw");
  });

  it("returns single character as-is", () => {
    expect(formatIssueId("a")).toBe("a");
  });

  it("returns exactly 10 char ID as-is", () => {
    expect(formatIssueId("1234567890")).toBe("1234567890");
  });

  it("returns typical loomcli ID as-is (13 chars)", () => {
    expect(formatIssueId("loomcli-pso6j")).toBe("loomcli-pso6j");
  });

  it("returns 14-char ID as-is", () => {
    expect(formatIssueId("issue-abcdefgh")).toBe("issue-abcdefgh");
  });

  it("returns 16-char ID as-is", () => {
    expect(formatIssueId("loomcli-a3f2dda8")).toBe("loomcli-a3f2dda8");
  });

  it("truncates ID that is exactly 17 chars", () => {
    expect(formatIssueId("some-abcdefghijkl")).toBe("some-abcde...");
  });

  it("truncates long hierarchical ID preserving prefix", () => {
    // 'loomcli-af78e9a2.1.2' = 20 chars
    expect(formatIssueId("loomcli-af78e9a2.1.2")).toBe("loomcli-af78e...");
  });

  it("truncates very long ID preserving prefix", () => {
    expect(formatIssueId("some-very-long-issue-id-12345")).toBe(
      "some-very-...",
    );
  });

  it("truncates long ID with no hyphen", () => {
    expect(formatIssueId("abcdefghijklmnopqrst")).toBe("abcdefghijklm...");
  });

  it("returns short ID without hyphen as-is", () => {
    expect(formatIssueId("loom-xyz")).toBe("loom-xyz");
  });

  it("warns in development for empty ID", () => {
    const originalEnv = process.env.NODE_ENV;
    process.env.NODE_ENV = "development";

    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    formatIssueId("");

    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining("empty issue ID"),
    );

    warnSpy.mockRestore();
    process.env.NODE_ENV = originalEnv;
  });
});
