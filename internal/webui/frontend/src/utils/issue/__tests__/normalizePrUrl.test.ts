import { describe, it, expect } from "vitest";

import { normalizePrUrl } from "@/utils/issue";

describe("normalizePrUrl", () => {
  it("normalizes pull URLs for matching", () => {
    expect(
      normalizePrUrl("https://github.com/org/repo/pull/42/"),
    ).toBe("https://github.com/org/repo/pull/42");
    expect(
      normalizePrUrl("https://github.com/org/repo/pull/42"),
    ).toBe("https://github.com/org/repo/pull/42");
  });

  it("returns null for non-PR refs", () => {
    expect(normalizePrUrl("JIRA-1")).toBeNull();
    expect(normalizePrUrl(null)).toBeNull();
  });
});
