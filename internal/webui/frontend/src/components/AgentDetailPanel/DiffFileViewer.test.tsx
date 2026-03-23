/**
 * @vitest-environment jsdom
 */

import { describe, it, expect } from "vitest";

import { parsePatch } from "./DiffFileViewer";

describe("parsePatch", () => {
  it("parses a simple single-hunk patch", () => {
    const patch = [
      "@@ -1,3 +1,4 @@",
      " unchanged",
      "-removed",
      "+added",
      " context",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    expect(lines).toHaveLength(5); // hunk header + 4 content lines
    expect(lines[0]).toMatchObject({ type: "hunk" });
    expect(lines[1]).toMatchObject({ type: "context", oldNum: 1, newNum: 1 });
    expect(lines[2]).toMatchObject({ type: "del", oldNum: 2 });
    expect(lines[3]).toMatchObject({ type: "add", newNum: 2 });
    expect(lines[4]).toMatchObject({ type: "context", oldNum: 3, newNum: 3 });
  });

  it("parses a multi-hunk patch", () => {
    const patch = [
      "@@ -1,2 +1,2 @@",
      "-old first",
      "+new first",
      " same",
      "@@ -10,2 +10,2 @@",
      "-old tenth",
      "+new tenth",
      " same",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(2);
    expect(result.hunks[0].lines[0]).toMatchObject({
      type: "hunk",
      content: "@@ -1,2 +1,2 @@",
    });
    expect(result.hunks[1].lines[0]).toMatchObject({
      type: "hunk",
      content: "@@ -10,2 +10,2 @@",
    });
  });

  it("does not create a new hunk for embedded @@ in file content", () => {
    const patch = [
      "@@ -1,4 +1,4 @@",
      " normal line",
      " @@ this is not a hunk header",
      "-old line",
      "+new line",
    ].join("\n");

    const result = parsePatch(patch);
    // Should still be a single hunk — the embedded @@ must not split it
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    // hunk header + 4 content lines = 5 total
    expect(lines).toHaveLength(5);

    // Line numbers should be continuous and correct
    expect(lines[1]).toMatchObject({ type: "context", oldNum: 1, newNum: 1 });
    // The " @@ ..." line is a context line (starts with space)
    expect(lines[2]).toMatchObject({ type: "context", oldNum: 2, newNum: 2 });
    expect(lines[3]).toMatchObject({ type: "del", oldNum: 3 });
    expect(lines[4]).toMatchObject({ type: "add", newNum: 3 });
  });

  it("treats raw @@ at column 0 as context when it doesn't match hunk format", () => {
    const patch = [
      "@@ -1,3 +1,3 @@",
      " first",
      "@@ not a valid hunk header",
      " last",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);

    const lines = result.hunks[0].lines;
    expect(lines).toHaveLength(4);
    expect(lines[1]).toMatchObject({ type: "context", oldNum: 1, newNum: 1 });
    // The invalid @@ line is treated as context, not a hunk header
    expect(lines[2]).toMatchObject({ type: "context", oldNum: 2, newNum: 2 });
    expect(lines[3]).toMatchObject({ type: "context", oldNum: 3, newNum: 3 });
  });

  it("ignores @@ before first valid hunk header", () => {
    const patch = [
      "@@ garbage that doesn't match",
      "@@ -1,2 +1,2 @@",
      " context",
      "+added",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    expect(result.hunks[0].lines).toHaveLength(3);
  });

  it("handles hunk header with trailing context (function name)", () => {
    const patch = [
      "@@ -1,3 +1,3 @@ func main()",
      " unchanged",
      "-old",
      "+new",
    ].join("\n");

    const result = parsePatch(patch);
    expect(result.hunks).toHaveLength(1);
    expect(result.hunks[0].header).toBe("@@ -1,3 +1,3 @@ func main()");
  });

  it("returns empty hunks for empty string", () => {
    const result = parsePatch("");
    expect(result.hunks).toHaveLength(0);
  });

  it("returns empty hunks when no hunk headers present", () => {
    const result = parsePatch("just some text\nno diff here");
    expect(result.hunks).toHaveLength(0);
  });
});
