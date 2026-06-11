import { describe, it, expect } from "vitest";

import { normalizePrUrl, prKeyFromRef } from "@/utils/issue";

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

describe("prKeyFromRef", () => {
  it("builds an owner/repo#number key from a canonical URL", () => {
    expect(prKeyFromRef("https://github.com/Org/Repo/pull/42")).toBe(
      "org/repo#42",
    );
  });

  it("is robust to URL variants that break string matching", () => {
    const expected = "org/repo#42";
    expect(prKeyFromRef("http://github.com/org/repo/pull/42")).toBe(expected);
    expect(prKeyFromRef("https://www.github.com/org/repo/pull/42")).toBe(
      expected,
    );
    expect(prKeyFromRef("https://github.com/org/repo.git/pull/42")).toBe(
      expected,
    );
    expect(prKeyFromRef("https://github.com/org/repo/pull/42/files")).toBe(
      expected,
    );
    expect(prKeyFromRef("https://github.com/org/repo/pull/42/")).toBe(
      expected,
    );
    // GitHub Enterprise-style "pulls" path
    expect(prKeyFromRef("https://github.com/org/repo/pulls/42")).toBe(
      expected,
    );
  });

  it("returns null for non-PR refs", () => {
    expect(prKeyFromRef("JIRA-1")).toBeNull();
    expect(prKeyFromRef("https://github.com/org/repo")).toBeNull();
    expect(prKeyFromRef("https://github.com/org/repo/issues/42")).toBeNull();
    expect(prKeyFromRef(null)).toBeNull();
    expect(prKeyFromRef(undefined)).toBeNull();
  });
});
