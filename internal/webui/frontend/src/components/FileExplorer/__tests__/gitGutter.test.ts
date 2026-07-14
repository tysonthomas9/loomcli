import { describe, expect, it } from "vitest";

import { computeGitGutterLineMarks } from "../gitGutter";

describe("computeGitGutterLineMarks", () => {
  it("marks changed and added lines from buffer vs base content", () => {
    const marks = computeGitGutterLineMarks(
      "one\ntwo\nthree\n",
      "one\nchanged\nthree\nfour\n",
    );

    expect(marks).toContainEqual({ line: 2, kind: "changed" });
    expect(marks.some((mark) => mark.kind === "added")).toBe(true);
  });

  it("marks a deletion on the following current-buffer line", () => {
    const marks = computeGitGutterLineMarks(
      "one\ntwo\nthree\n",
      "one\nthree\n",
    );

    expect(marks.some((mark) => mark.kind === "deleted")).toBe(true);
  });
});
