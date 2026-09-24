import { describe, it, expect } from "vitest";

import {
  formatPrKey,
  parsePrKey,
  prKeyFromRef,
  pullRequestKey,
} from "@/utils/issue";

describe("formatPrKey", () => {
  it("builds a lowercased github:owner/repo#number key", () => {
    expect(formatPrKey("octocat", "hello", 42)).toBe("github:octocat/hello#42");
    expect(formatPrKey("OctoCat", "Hello-World", 7)).toBe(
      "github:octocat/hello-world#7",
    );
    expect(formatPrKey("  my_org ", " repo.js ", 3)).toBe(
      "github:my_org/repo.js#3",
    );
  });

  it.each([
    ["", "hello", 1],
    ["octo", "", 1],
    ["octo/cat", "hello", 1],
    ["octo", "hel#lo", 1],
    ["octo", "hel lo", 1],
    [".", "hello", 1],
    ["octo", "..", 1],
    ["octo", "hello", 0],
    ["octo", "hello", -3],
    ["octo", "hello", 1.5],
    ["octo", "hello", Number.NaN],
  ])("returns null for invalid input (%j, %j, %j)", (owner, repo, number) => {
    expect(formatPrKey(owner, repo, number)).toBeNull();
  });
});

describe("parsePrKey", () => {
  it.each([
    [
      "github:octocat/hello#42",
      { owner: "octocat", repo: "hello", number: 42 },
    ],
    ["octocat/hello#42", { owner: "octocat", repo: "hello", number: 42 }],
    [
      "GITHUB:octocat/hello#42",
      { owner: "octocat", repo: "hello", number: 42 },
    ],
    [
      "github:OctoCat/Hello.World#5",
      { owner: "octocat", repo: "hello.world", number: 5 },
    ],
    ["  github:octo/hello#1  ", { owner: "octo", repo: "hello", number: 1 }],
  ])("parses %j", (key, expected) => {
    expect(parsePrKey(key)).toEqual(expected);
  });

  it.each([
    null,
    undefined,
    "",
    "github:",
    "github:octo/hello",
    "github:octo/hello#",
    "github:octohello#1",
    "github:/hello#1",
    "github:octo/#1",
    "github:octo/hello#042",
    "github:octo/hello#0",
    "github:octo/hello#-3",
    "github:octo/hello#abc",
    "github:octo/hello#3x",
    "github:octo/hello/extra#3",
    "github:../hello#3",
    "github:octo/..#3",
    "gitlab:octo/hello#3",
  ])("rejects %j", (key) => {
    expect(parsePrKey(key)).toBeNull();
  });

  it("round-trips formatPrKey output", () => {
    const key = formatPrKey("Octo-Org", "Hello.World", 123456);
    expect(key).not.toBeNull();
    const parsed = parsePrKey(key);
    expect(parsed).not.toBeNull();
    expect(formatPrKey(parsed!.owner, parsed!.repo, parsed!.number)).toBe(key);
  });
});

describe("pullRequestKey", () => {
  it("prefers the server-issued pr_key, canonicalized", () => {
    expect(
      pullRequestKey({
        pr_key: "GITHUB:Octo/Hello#7",
        url: "https://github.com/someone-else/fork/pull/99",
      }),
    ).toBe("github:octo/hello#7");
  });

  it("accepts a legacy unprefixed pr_key", () => {
    expect(pullRequestKey({ pr_key: "octo/hello#7" })).toBe(
      "github:octo/hello#7",
    );
  });

  it("falls back to the URL when pr_key is absent or invalid", () => {
    const url = "https://www.github.com/Octo/Hello/pull/7/files";
    expect(pullRequestKey({ url })).toBe("github:octo/hello#7");
    expect(pullRequestKey({ pr_key: null, url })).toBe("github:octo/hello#7");
    expect(pullRequestKey({ pr_key: "", url })).toBe("github:octo/hello#7");
    expect(pullRequestKey({ pr_key: "garbage", url })).toBe(
      "github:octo/hello#7",
    );
  });

  it("matches prKeyFromRef for the same PR", () => {
    expect(pullRequestKey({ pr_key: "github:org/repo#42" })).toBe(
      prKeyFromRef("https://github.com/Org/Repo/pull/42"),
    );
  });

  it("returns null when neither pr_key nor URL identify a PR", () => {
    expect(pullRequestKey({})).toBeNull();
    expect(
      pullRequestKey({ pr_key: null, url: "https://github.com/org/repo" }),
    ).toBeNull();
  });
});
